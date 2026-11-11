package main

import (
	"archive/zip"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxExpanded = int64(8 << 30)

type Candidate struct {
	Root    string `json:"root"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
	HasIcon bool   `json:"hasIcon"`
}
type Stage struct {
	ID         string      `json:"id"`
	Candidates []Candidate `json:"candidates"`
}

func safeArchivePath(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.ContainsAny(name, ":\x00") {
		return "", fmt.Errorf("压缩包含非法路径")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("压缩包路径越界")
		}
		if strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
			return "", fmt.Errorf("压缩包包含不兼容 Windows 的路径")
		}
		stem := strings.ToUpper(strings.Split(part, ".")[0])
		if reservedWindows.MatchString(stem) {
			return "", fmt.Errorf("压缩包包含 Windows 保留文件名")
		}
	}
	clean := path.Clean(name)
	if clean == "." {
		return "", nil
	}
	return filepath.FromSlash(clean), nil
}

var reservedWindows = regexp.MustCompile(`^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])$`)

func extractMap(archive, dest string) ([]Candidate, error) {
	z, e := zip.OpenReader(archive)
	if e != nil {
		return nil, fmt.Errorf("无法读取 ZIP：%w", e)
	}
	defer z.Close()
	if len(z.File) > 100000 {
		return nil, fmt.Errorf("压缩包文件过多，上限 100000 个")
	}
	var total int64
	seen := map[string]bool{}
	for _, f := range z.File {
		rel, e := safeArchivePath(f.Name)
		if e != nil {
			return nil, e
		}
		if rel == "" {
			continue
		}
		key := strings.ToLower(rel)
		if seen[key] {
			return nil, fmt.Errorf("压缩包包含重复路径：%s", f.Name)
		}
		seen[key] = true
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return nil, fmt.Errorf("不支持压缩包中的链接或特殊文件")
		}
		if f.UncompressedSize64 > uint64(maxExpanded-total) {
			return nil, fmt.Errorf("解压后大小超过 8 GB")
		}
		total += int64(f.UncompressedSize64)
	}
	var roots []string
	for _, f := range z.File {
		rel, _ := safeArchivePath(f.Name)
		if rel == "" {
			continue
		}
		target := filepath.Join(dest, rel)
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(target, 0755); e != nil {
				return nil, e
			}
			continue
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return nil, e
		}
		src, e := f.Open()
		if e != nil {
			return nil, e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			src.Close()
			return nil, e
		}
		n, copyErr := io.Copy(out, io.LimitReader(src, int64(f.UncompressedSize64)+1))
		closeErr := out.Close()
		src.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if n != int64(f.UncompressedSize64) {
			return nil, fmt.Errorf("ZIP 文件大小不符")
		}
		if filepath.Base(rel) == "level.dat" {
			roots = append(roots, filepath.Dir(rel))
		}
	}
	sort.Strings(roots)
	result := []Candidate{}
	for _, root := range roots {
		dir := filepath.Join(dest, root)
		name := filepath.Base(root)
		if root == "." {
			name = strings.TrimSuffix(filepath.Base(archive), ".zip")
		}
		version, source, levelName := detectWorld(dir)
		if levelName != "" {
			name = stripFormatting(levelName)
		}
		if name == "" {
			name = "未命名地图"
		}
		_, iconErr := os.Stat(filepath.Join(dir, "icon.png"))
		result = append(result, Candidate{Root: root, Name: name, Version: version, Source: source, HasIcon: iconErr == nil})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("未找到 level.dat，这个 ZIP 不是 Java 版地图存档")
	}
	return result, nil
}
func stripFormatting(s string) string { return regexp.MustCompile(`§.`).ReplaceAllString(s, "") }
func detectWorld(dir string) (version, source, name string) {
	if b, e := os.ReadFile(filepath.Join(dir, "mcversion.txt")); e == nil && len(b) < 256 {
		v := strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff"))
		if validVersion.MatchString(v) {
			version = v
			source = "mcversion.txt"
		}
	}
	b, e := os.ReadFile(filepath.Join(dir, "level.dat"))
	if e != nil {
		return
	}
	zr, e := gzip.NewReader(strings.NewReader(string(b)))
	if e != nil {
		return
	}
	defer zr.Close()
	raw, e := io.ReadAll(io.LimitReader(zr, 16<<20))
	if e != nil {
		return
	}
	node, e := readNBT(raw)
	if e != nil {
		return
	}
	data, _ := node["Data"].(map[string]any)
	name, _ = data["LevelName"].(string)
	if version == "" {
		ver, _ := data["Version"].(map[string]any)
		v, _ := ver["Name"].(string)
		if validVersion.MatchString(v) {
			version = v
			source = "level.dat（最后保存版本，请确认地图要求）"
		} else if v != "" {
			version = normalizeVersion(v)
			source = "level.dat（最后保存版本，请确认地图要求）"
		}
	}
	return
}
func normalizeVersion(v string) string {
	v = strings.ReplaceAll(v, " Release Candidate ", "-rc-")
	v = strings.ReplaceAll(v, " Pre-Release ", "-pre-")
	if validVersion.MatchString(v) {
		return v
	}
	return ""
}

// Small bounded NBT reader: only used to inspect level.dat; map files are never executed.
type nbtReader struct {
	b         []byte
	at, nodes int
}

func readNBT(b []byte) (map[string]any, error) {
	r := &nbtReader{b: b}
	t, e := r.take(1)
	if e != nil {
		return nil, e
	}
	if t[0] != 10 {
		return nil, fmt.Errorf("NBT 根标签不是 compound")
	}
	if _, e = r.str(); e != nil {
		return nil, e
	}
	v, e := r.value(10, 0)
	m, _ := v.(map[string]any)
	return m, e
}
func (r *nbtReader) take(n int) ([]byte, error) {
	if n < 0 || n > len(r.b)-r.at {
		return nil, io.ErrUnexpectedEOF
	}
	b := r.b[r.at : r.at+n]
	r.at += n
	return b, nil
}
func (r *nbtReader) str() (string, error) {
	b, e := r.take(2)
	if e != nil {
		return "", e
	}
	b, e = r.take(int(binary.BigEndian.Uint16(b)))
	return string(b), e
}
func (r *nbtReader) count() (int, error) {
	b, e := r.take(4)
	if e != nil {
		return 0, e
	}
	n := int(int32(binary.BigEndian.Uint32(b)))
	if n < 0 || n > 1<<24 {
		return 0, fmt.Errorf("NBT 长度无效")
	}
	return n, nil
}
func (r *nbtReader) value(t byte, depth int) (any, error) {
	r.nodes++
	if depth > 64 || r.nodes > 200000 {
		return nil, fmt.Errorf("NBT 过于复杂")
	}
	switch t {
	case 1, 2, 3, 4, 5, 6:
		n := map[byte]int{1: 1, 2: 2, 3: 4, 4: 8, 5: 4, 6: 8}[t]
		_, e := r.take(n)
		return nil, e
	case 8:
		return r.str()
	case 7, 11, 12:
		n, e := r.count()
		if e != nil {
			return nil, e
		}
		size := map[byte]int{7: 1, 11: 4, 12: 8}[t]
		_, e = r.take(n * size)
		return nil, e
	case 9:
		typ, e := r.take(1)
		if e != nil {
			return nil, e
		}
		n, e := r.count()
		if e != nil {
			return nil, e
		}
		if n > 200000 {
			return nil, fmt.Errorf("NBT 列表过长")
		}
		for i := 0; i < n; i++ {
			if _, e = r.value(typ[0], depth+1); e != nil {
				return nil, e
			}
		}
		return nil, nil
	case 10:
		m := map[string]any{}
		for {
			b, e := r.take(1)
			if e != nil {
				return nil, e
			}
			if b[0] == 0 {
				return m, nil
			}
			name, e := r.str()
			if e != nil {
				return nil, e
			}
			v, e := r.value(b[0], depth+1)
			if e != nil {
				return nil, e
			}
			m[name] = v
		}
	}
	return nil, fmt.Errorf("未知 NBT 标签")
}

func (a *App) commitStage(stageID string, index int, name, description, version string) (World, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	stage := a.stages[stageID]
	if stage == nil || index < 0 || index >= len(stage.Candidates) {
		return World{}, fmt.Errorf("导入已失效，请重新拖入 ZIP")
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || len(description) > 4000 {
		return World{}, fmt.Errorf("请填写地图名称，简介最多 4000 字节")
	}
	if !validVersion.MatchString(version) {
		return World{}, fmt.Errorf("请填写有效版本，如 26.3 或 26.3-rc-3")
	}
	w := World{ID: newID(), Name: name, Description: description, Version: version, Added: time.Now().Format(time.RFC3339), Engine: "vanilla"}
	dest := a.instance(w.ID)
	if e := os.MkdirAll(dest, 0755); e != nil {
		return World{}, e
	}
	src := filepath.Join(a.root, "staging", stageID, "extracted", stage.Candidates[index].Root)
	if e := os.Rename(src, filepath.Join(dest, "world")); e != nil {
		os.Remove(dest)
		return World{}, e
	}
	old := a.db.Worlds
	a.db.Worlds = append(a.db.Worlds, w)
	if e := a.saveLocked(); e != nil {
		a.db.Worlds = old
		_ = os.Rename(filepath.Join(dest, "world"), src)
		return World{}, e
	}
	delete(a.stages, stageID)
	_ = os.RemoveAll(filepath.Join(a.root, "staging", stageID))
	return w, nil
}

var errJarNoVersion = errors.New("无法识别 JAR 内部版本；请使用官方下载或导入现代官方服务端")

func jarVersion(file string) (string, error) {
	z, e := zip.OpenReader(file)
	if e != nil {
		return "", fmt.Errorf("不是有效 JAR 文件")
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name == "META-INF/versions.list" {
			r, e := f.Open()
			if e != nil {
				return "", e
			}
			b, e := io.ReadAll(io.LimitReader(r, 1<<20))
			r.Close()
			if e != nil {
				return "", e
			}
			fields := strings.Fields(string(b))
			if len(fields) >= 2 && validVersion.MatchString(fields[1]) {
				return fields[1], nil
			}
		}
		if f.Name == "version.json" {
			r, e := f.Open()
			if e != nil {
				return "", e
			}
			var v struct {
				ID string `json:"id"`
			}
			e = json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&v)
			r.Close()
			if e == nil && validVersion.MatchString(v.ID) {
				return v.ID, nil
			}
		}
	}
	return "", errJarNoVersion
}
