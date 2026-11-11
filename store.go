package main

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type World struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Added       string `json:"added"`
	Engine      string `json:"engine"`
}
type Settings struct {
	Java      string   `json:"java"`
	Memory    int      `json:"memory"`
	Port      int      `json:"port"`
	Operators []string `json:"operators"`
	EULA      bool     `json:"eula"`
}
type Database struct {
	Worlds   []World  `json:"worlds"`
	Settings Settings `json:"settings"`
}
type App struct {
	mu                   sync.Mutex
	root, address, token string
	db                   Database
	run                  *Run
	logs                 []string
	tasks                map[string]*Task
	stages               map[string]*Stage
	manifest             *Manifest
	manifestAt           time.Time
	shutdown             func()
	closing              bool
}

var validVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
var validPlayer = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)

func newApp(root string) (*App, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	a := &App{root: root, tasks: map[string]*Task{}, stages: map[string]*Stage{}, logs: []string{}}
	for _, dir := range []string{"instances", "servers", "staging", "backups"} {
		if err = os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			return nil, err
		}
	}
	// Staging is transient; leave previous failed imports on disk rather than deleting unknown data.
	a.db = Database{Worlds: []World{}, Settings: Settings{Java: detectJava(), Memory: 2, Port: 25565, Operators: []string{}}}
	b, err := os.ReadFile(filepath.Join(root, "library.json"))
	if err == nil {
		if err = json.Unmarshal(b, &a.db); err != nil {
			return nil, fmt.Errorf("配置文件无法读取，原文件已保留：%w", err)
		}
		var legacy struct {
			Settings struct {
				Operator  string          `json:"operator"`
				Operators json.RawMessage `json:"operators"`
			} `json:"settings"`
		}
		if json.Unmarshal(b, &legacy) == nil && len(legacy.Settings.Operators) == 0 && validPlayer.MatchString(legacy.Settings.Operator) {
			a.db.Settings.Operators = []string{legacy.Settings.Operator}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if a.db.Worlds == nil {
		a.db.Worlds = []World{}
	}
	if a.db.Settings.Operators == nil {
		a.db.Settings.Operators = []string{}
	}
	for i := range a.db.Worlds {
		if a.db.Worlds[i].Engine == "" {
			a.db.Worlds[i].Engine = "vanilla"
		}
	}
	return a, nil
}
func detectJava() string {
	p := filepath.Join(os.Getenv("APPDATA"), ".minecraft", "runtime", "java-runtime-epsilon", "bin", "java.exe")
	if _, e := os.Stat(p); e == nil {
		return p
	}
	if p := os.Getenv("JAVA_HOME"); p != "" {
		for _, n := range []string{"java.exe", "java"} {
			f := filepath.Join(p, "bin", n)
			if _, e := os.Stat(f); e == nil {
				return f
			}
		}
	}
	if p, e := exec.LookPath("java"); e == nil {
		return p
	}
	return "java"
}
func (a *App) saveLocked() error { return atomicJSON(filepath.Join(a.root, "library.json"), a.db) }
func atomicJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(path, b)
}
func atomicWrite(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".write-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func newID() string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (a *App) findLocked(id string) (World, error) {
	for _, w := range a.db.Worlds {
		if w.ID == id {
			return w, nil
		}
	}
	return World{}, fmt.Errorf("地图不存在")
}
func (a *App) instance(id string) string { return filepath.Join(a.root, "instances", id) }
func offlineUUID(name string) string {
	b := md5.Sum([]byte("OfflinePlayer:" + name))
	b[6] = (b[6] & 15) | 48
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
func validateSettings(s Settings) error {
	if strings.TrimSpace(s.Java) == "" {
		return fmt.Errorf("请填写 Java 路径")
	}
	if s.Memory < 1 || s.Memory > 64 {
		return fmt.Errorf("最大内存应为 1–64 GB")
	}
	if s.Port < 1024 || s.Port > 65535 {
		return fmt.Errorf("端口应为 1024–65535")
	}
	if len(s.Operators) > 64 {
		return fmt.Errorf("最多配置 64 个管理员")
	}
	seen := map[string]bool{}
	for _, name := range s.Operators {
		if !validPlayer.MatchString(name) {
			return fmt.Errorf("管理员 %q 名称无效，应为 1–16 位英文、数字或下划线", name)
		}
		key := strings.ToLower(name)
		if seen[key] {
			return fmt.Errorf("管理员列表中有重复名称：%s", name)
		}
		seen[key] = true
	}
	return nil
}
