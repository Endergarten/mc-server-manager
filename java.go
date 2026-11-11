package main

import (
	"archive/zip"
	"bytes"
	"context"
	"debug/pe"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed config/java-rules.json
var defaultJavaRules []byte

type JavaRuntime struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Major   int    `json:"major"`
	Arch    string `json:"arch"`
	Vendor  string `json:"vendor"`
	Source  string `json:"source"`
}
type JavaRule struct {
	Versions  []string `json:"versions,omitempty"`
	Min       string   `json:"min,omitempty"`
	Before    string   `json:"before,omitempty"`
	Preferred int      `json:"preferred"`
	Allowed   []int    `json:"allowed"`
	Note      string   `json:"note,omitempty"`
}
type JavaRules struct {
	Overrides []JavaRule `json:"overrides"`
	Fallbacks []JavaRule `json:"fallbacks"`
}
type JavaRequirement struct {
	Preferred int    `json:"preferred"`
	Allowed   []int  `json:"allowed"`
	Source    string `json:"source"`
}

var javaNumber = regexp.MustCompile(`\d+`)
var quotedJavaVersion = regexp.MustCompile(`(?:version|openjdk)\s+"?([0-9][0-9A-Za-z._+\-]*)`)

func javaMajor(v string) int {
	v = strings.Trim(v, "\"")
	p := strings.Split(v, ".")
	i := 0
	if len(p) > 1 && p[0] == "1" {
		i = 1
	}
	digits := javaNumber.FindString(p[i])
	n, _ := strconv.Atoi(digits)
	return n
}
func javaArchitecture(v string) string {
	switch strings.ToLower(v) {
	case "x86_64", "amd64", "x64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "x86", "i386", "i586", "i686":
		return "386"
	}
	return v
}
func inspectJava(path, source string) (JavaRuntime, error) {
	p, e := filepath.Abs(strings.TrimSpace(strings.Trim(path, "\"")))
	if e != nil {
		return JavaRuntime{}, e
	}
	if resolved, e := filepath.EvalSymlinks(p); e == nil {
		p = resolved
	}
	name := strings.ToLower(filepath.Base(p))
	if name != "java.exe" && name != "java" {
		return JavaRuntime{}, fmt.Errorf("请选择 java.exe 程序")
	}
	if s, e := os.Stat(p); e != nil || s.IsDir() {
		return JavaRuntime{}, fmt.Errorf("Java 程序不存在")
	}
	j := JavaRuntime{Path: p, Source: source}
	release := filepath.Join(filepath.Dir(filepath.Dir(p)), "release")
	if b, e := os.ReadFile(release); e == nil {
		values := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "\"")
			}
		}
		j.Version = values["JAVA_VERSION"]
		j.Arch = javaArchitecture(values["OS_ARCH"])
		j.Vendor = values["IMPLEMENTOR"]
	}
	if j.Arch == "" && runtime.GOOS == "windows" {
		if binary, e := pe.Open(p); e == nil {
			switch binary.Machine {
			case pe.IMAGE_FILE_MACHINE_AMD64:
				j.Arch = "amd64"
			case pe.IMAGE_FILE_MACHINE_ARM64:
				j.Arch = "arm64"
			case pe.IMAGE_FILE_MACHINE_I386:
				j.Arch = "386"
			}
			binary.Close()
		}
	}
	if j.Version == "" || j.Arch == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, p, "-XshowSettings:properties", "-version")
		hideWindow(cmd)
		b, e := cmd.CombinedOutput()
		if e != nil {
			return j, fmt.Errorf("Java 无法运行：%w", e)
		}
		match := quotedJavaVersion.FindSubmatch(b)
		if len(match) > 1 {
			j.Version = string(match[1])
		}
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "os.arch":
				j.Arch = javaArchitecture(strings.TrimSpace(v))
			case "java.vendor":
				j.Vendor = strings.TrimSpace(v)
			}
		}
	}
	j.Major = javaMajor(j.Version)
	if j.Major < 6 {
		return j, fmt.Errorf("无法识别这个 Java 的版本")
	}
	return j, nil
}
func (a *App) initJava() error {
	rulesPath := filepath.Join(a.root, "java-rules.json")
	b, e := os.ReadFile(rulesPath)
	if os.IsNotExist(e) {
		b = defaultJavaRules
		if e = atomicWrite(rulesPath, b); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &a.javaRules); e != nil {
		return fmt.Errorf("java-rules.json 格式错误：%w", e)
	}
	if e = validateJavaRules(a.javaRules); e != nil {
		return e
	}
	var cached []JavaRuntime
	if b, e := os.ReadFile(filepath.Join(a.root, "java-cache.json")); e == nil {
		_ = json.Unmarshal(b, &cached)
	}
	for _, j := range cached {
		if _, e := os.Stat(j.Path); e == nil && j.Major >= 6 {
			a.javas = append(a.javas, j)
		}
	}
	if j, e := inspectJava(a.db.Settings.Java, "已有配置"); e == nil {
		a.mergeJavaLocked(j)
	}
	return nil
}
func validateJavaRules(r JavaRules) error {
	for _, rule := range append(append([]JavaRule{}, r.Overrides...), r.Fallbacks...) {
		if rule.Preferred < 6 || len(rule.Allowed) == 0 {
			return fmt.Errorf("Java 规则必须包含 preferred 和 allowed")
		}
		ok := false
		for _, n := range rule.Allowed {
			if n < 6 || n > 100 {
				return fmt.Errorf("Java 规则版本号无效")
			}
			if n == rule.Preferred {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("preferred 必须包含在 allowed 中")
		}
		if rule.Min != "" && !releaseNumbers(rule.Min).valid {
			return fmt.Errorf("Java 规则 min 必须是正式版本号")
		}
		if rule.Before != "" && !releaseNumbers(rule.Before).valid {
			return fmt.Errorf("Java 规则 before 必须是正式版本号")
		}
	}
	return nil
}
func (a *App) mergeJavaLocked(j JavaRuntime) {
	for i, old := range a.javas {
		if strings.EqualFold(old.Path, j.Path) {
			a.javas[i] = j
			return
		}
	}
	a.javas = append(a.javas, j)
}
func (a *App) startJavaScan() {
	a.mu.Lock()
	if a.javaScanning {
		a.mu.Unlock()
		return
	}
	a.javaScanning = true
	a.javaScanError = ""
	roots := append([]string{}, a.db.Settings.JavaRoots...)
	hint := a.db.Settings.Java
	a.mu.Unlock()
	go func() {
		roots = append(roots, platformJavaRoots()...)
		exe, _ := os.Executable()
		roots = append(roots, filepath.Dir(exe), filepath.Dir(filepath.Dir(exe)))
		candidates := map[string]string{}
		add := func(p, source string) {
			if p != "" {
				candidates[filepath.Clean(p)] = source
			}
		}
		if hint != "" {
			add(hint, "已有配置")
		}
		if p, e := exec.LookPath("java"); e == nil {
			add(p, "PATH")
		}
		if home := os.Getenv("JAVA_HOME"); home != "" {
			add(filepath.Join(home, "bin", javaExecutable()), "JAVA_HOME")
		}
		deadline := time.Now().Add(60 * time.Second)
		visited := 0
		seen := map[string]bool{}
		for _, root := range roots {
			root = filepath.Clean(root)
			key := strings.ToLower(root)
			if root == "." || seen[key] {
				continue
			}
			seen[key] = true
			_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				visited++
				if time.Now().After(deadline) || visited > 100000 {
					return fs.SkipAll
				}
				rel, _ := filepath.Rel(root, p)
				if d.IsDir() {
					if strings.Count(rel, string(filepath.Separator)) > 7 {
						return fs.SkipDir
					}
					switch strings.ToLower(d.Name()) {
					case "node_modules", ".git", "go-mod", "go-cache", "libraries", "assets", "logs", "backups", "saves", "world", "versions", "datapacks", "shaderpacks", "resourcepacks":
						return fs.SkipDir
					}
					return nil
				}
				if strings.EqualFold(d.Name(), javaExecutable()) && strings.EqualFold(filepath.Base(filepath.Dir(p)), "bin") {
					add(p, "自动搜索")
				}
				return nil
			})
		}
		for p, source := range candidates {
			if j, e := inspectJava(p, source); e == nil {
				a.mu.Lock()
				a.mergeJavaLocked(j)
				a.mu.Unlock()
			}
		}
		a.mu.Lock()
		sort.Slice(a.javas, func(i, j int) bool {
			if a.javas[i].Major != a.javas[j].Major {
				return a.javas[i].Major > a.javas[j].Major
			}
			return a.javas[i].Path < a.javas[j].Path
		})
		_ = atomicJSON(filepath.Join(a.root, "java-cache.json"), a.javas)
		a.javaScanning = false
		if visited > 100000 || time.Now().After(deadline) {
			a.javaScanError = "常见位置扫描已完成；如有遗漏，可选择具体文件或搜索目录。"
		}
		a.mu.Unlock()
	}()
}
func javaExecutable() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

type releaseVersion struct {
	parts [3]int
	valid bool
}

func releaseNumbers(v string) releaseVersion {
	var result releaseVersion
	p := strings.Split(v, ".")
	if len(p) < 2 || len(p) > 3 {
		return result
	}
	for i, s := range p {
		n, e := strconv.Atoi(s)
		if e != nil || n < 0 {
			return result
		}
		result.parts[i] = n
	}
	result.valid = true
	return result
}
func releaseCompare(a, b releaseVersion) int {
	for i := 0; i < 3; i++ {
		if a.parts[i] < b.parts[i] {
			return -1
		}
		if a.parts[i] > b.parts[i] {
			return 1
		}
	}
	return 0
}
func ruleMatches(r JavaRule, version string) bool {
	for _, v := range r.Versions {
		if v == version {
			return true
		}
	}
	if len(r.Versions) > 0 {
		return false
	}
	v := releaseNumbers(version)
	if !v.valid {
		return false
	}
	if r.Min != "" && releaseCompare(v, releaseNumbers(r.Min)) < 0 {
		return false
	}
	if r.Before != "" && releaseCompare(v, releaseNumbers(r.Before)) >= 0 {
		return false
	}
	return r.Min != "" || r.Before != ""
}
func (a *App) javaRequirement(version string) (JavaRequirement, error) {
	// Re-read user-editable rules so changes apply without restarting the manager.
	rules := a.javaRules
	if b, e := os.ReadFile(filepath.Join(a.root, "java-rules.json")); e == nil {
		if e = json.Unmarshal(b, &rules); e != nil {
			return JavaRequirement{}, fmt.Errorf("Java 兼容规则 JSON 无效")
		}
		if e = validateJavaRules(rules); e != nil {
			return JavaRequirement{}, e
		}
	}
	for _, r := range rules.Overrides {
		if ruleMatches(r, version) {
			return JavaRequirement{r.Preferred, r.Allowed, "用户兼容规则"}, nil
		}
	}
	var meta VersionMeta
	if b, e := os.ReadFile(filepath.Join(filepath.Dir(a.serverJar(version)), "metadata.json")); e == nil && json.Unmarshal(b, &meta) == nil && meta.ID == version && meta.JavaVersion.Major >= 6 {
		m := meta.JavaVersion.Major
		return JavaRequirement{m, []int{m}, "Mojang 官方版本信息"}, nil
	}
	if m := javaMajorInJar(a.serverJar(version)); m >= 6 {
		return JavaRequirement{m, []int{m}, "官方服务端 version.json"}, nil
	}
	for _, r := range rules.Fallbacks {
		if ruleMatches(r, version) {
			return JavaRequirement{r.Preferred, r.Allowed, "内置兼容规则（可编辑）"}, nil
		}
	}
	return JavaRequirement{}, fmt.Errorf("无法确认 %s 需要的 Java，请下载官方服务端元信息或在 java-rules.json 中添加规则", version)
}
func javaMajorInJar(file string) int {
	z, e := zip.OpenReader(file)
	if e != nil {
		return 0
	}
	defer z.Close()
	if m := javaMajorFromZip(&z.Reader); m > 0 {
		return m
	}
	for _, f := range z.File {
		if strings.HasPrefix(f.Name, "META-INF/versions/") && strings.HasSuffix(f.Name, ".jar") && f.UncompressedSize64 < 128<<20 {
			r, e := f.Open()
			if e != nil {
				continue
			}
			b, e := io.ReadAll(io.LimitReader(r, 128<<20))
			r.Close()
			if e != nil {
				continue
			}
			inner, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
			if e == nil {
				return javaMajorFromZip(inner)
			}
		}
	}
	return 0
}
func javaMajorFromZip(z *zip.Reader) int {
	for _, f := range z.File {
		if f.Name == "version.json" {
			r, e := f.Open()
			if e != nil {
				return 0
			}
			var v struct {
				Java int `json:"java_version"`
			}
			e = json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&v)
			r.Close()
			if e == nil {
				return v.Java
			}
		}
	}
	return 0
}
func chooseJava(found []JavaRuntime, req JavaRequirement) (JavaRuntime, error) {
	var choices []JavaRuntime
	for _, j := range found {
		if _, e := os.Stat(j.Path); e != nil {
			continue
		}
		if runtime.GOARCH == "amd64" && j.Arch != "amd64" {
			continue
		}
		if runtime.GOARCH == "arm64" && j.Arch != "arm64" && j.Arch != "amd64" {
			continue
		}
		for _, n := range req.Allowed {
			if j.Major == n {
				choices = append(choices, j)
				break
			}
		}
	}
	if len(choices) == 0 {
		return JavaRuntime{}, fmt.Errorf("未找到兼容的 64 位 Java，需要 Java %v；请到“Java 环境”重新扫描，或浏览选择 java.exe / 搜索文件夹", req.Allowed)
	}
	sort.SliceStable(choices, func(i, j int) bool {
		if (choices[i].Major == req.Preferred) != (choices[j].Major == req.Preferred) {
			return choices[i].Major == req.Preferred
		}
		if choices[i].Major != choices[j].Major {
			return choices[i].Major > choices[j].Major
		}
		return compareJavaVersion(choices[i].Version, choices[j].Version) > 0
	})
	return choices[0], nil
}
func compareJavaVersion(a, b string) int {
	x, y := javaNumber.FindAllString(a, -1), javaNumber.FindAllString(b, -1)
	for i := 0; i < len(x) || i < len(y); i++ {
		u, v := 0, 0
		if i < len(x) {
			u, _ = strconv.Atoi(x[i])
		}
		if i < len(y) {
			v, _ = strconv.Atoi(y[i])
		}
		if u < v {
			return -1
		}
		if u > v {
			return 1
		}
	}
	return 0
}
