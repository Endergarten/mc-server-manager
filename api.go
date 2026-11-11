package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (a *App) routes(m *http.ServeMux) {
	m.HandleFunc("/api/state", a.state)
	m.HandleFunc("/api/settings", a.settings)
	m.HandleFunc("/api/java/scan", a.scanJava)
	m.HandleFunc("/api/java/browse", a.browseJava)
	m.HandleFunc("/api/java/requirement", a.javaRequirementAPI)
	m.HandleFunc("/api/import/inspect", a.inspect)
	m.HandleFunc("/api/import/commit", a.commit)
	m.HandleFunc("/api/import/cancel", a.cancelImport)
	m.HandleFunc("/api/world/edit", a.editWorld)
	m.HandleFunc("/api/world/start", a.start)
	m.HandleFunc("/api/world/backup", a.backup)
	m.HandleFunc("/api/world/delete", a.deleteWorldAPI)
	m.HandleFunc("/api/world/icon", a.icon)
	m.HandleFunc("/api/server/stop", a.stop)
	m.HandleFunc("/api/server/command", a.sendCommand)
	m.HandleFunc("/api/versions", a.versions)
	m.HandleFunc("/api/versions/download", a.downloadVersion)
	m.HandleFunc("/api/versions/import", a.importJar)
	m.HandleFunc("/api/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, "POST") {
			return
		}
		respond(w, map[string]bool{"ok": true})
		a.shutdown()
	})
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		w.Header().Set("Allow", want)
		http.Error(w, "请求方法不支持", 405)
		return false
	}
	return true
}
func (a *App) state(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	installed := []string{}
	entries, _ := os.ReadDir(filepath.Join(a.root, "servers"))
	for _, e := range entries {
		if e.IsDir() && a.installed(e.Name()) {
			installed = append(installed, e.Name())
		}
	}
	tasks := []Task{}
	for _, t := range a.tasks {
		tasks = append(tasks, *t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Version < tasks[j].Version })
	respond(w, map[string]any{"token": a.token, "worlds": a.db.Worlds, "settings": a.db.Settings, "server": a.statusLocked(), "logs": a.logs, "installed": installed, "tasks": tasks, "dataPath": a.root, "desktop": a.desktop, "javas": a.javas, "javaScanning": a.javaScanning, "javaScanError": a.javaScanError})
}
func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var s Settings
	if e := decode(w, r, &s); e != nil {
		fail(w, e)
		return
	}
	if s.ServerOptions == nil {
		s.ServerOptions = defaultServerOptions()
	}
	if e := validateSettings(s); e != nil {
		fail(w, e)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	old := a.db.Settings
	a.db.Settings = s
	if e := a.saveLocked(); e != nil {
		a.db.Settings = old
		fail(w, e)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (a *App) receiveFile(w http.ResponseWriter, r *http.Request, limit int64) (string, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	reader, e := r.MultipartReader()
	if e != nil {
		return "", "", fmt.Errorf("请上传文件")
	}
	part, e := reader.NextPart()
	if e != nil {
		return "", "", e
	}
	defer part.Close()
	if part.FileName() == "" {
		return "", "", fmt.Errorf("未选择文件")
	}
	f, e := os.CreateTemp(filepath.Join(a.root, "staging"), "upload-*")
	if e != nil {
		return "", "", e
	}
	dest := f.Name()
	_, e = io.Copy(f, part)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		os.Remove(dest)
		return "", "", e
	}
	return dest, part.FileName(), nil
}
func (a *App) inspect(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	file, name, e := a.receiveFile(w, r, 2<<30)
	if e != nil {
		fail(w, e)
		return
	}
	defer os.Remove(file)
	if !strings.EqualFold(filepath.Ext(name), ".zip") {
		fail(w, fmt.Errorf("请选择地图 ZIP 文件"))
		return
	}
	stage, e := a.inspectMapPath(file)
	if e != nil {
		fail(w, e)
		return
	}
	for i := range stage.Candidates {
		if stage.Candidates[i].Root == "." && strings.HasPrefix(stage.Candidates[i].Name, "upload-") {
			stage.Candidates[i].Name = strings.TrimSuffix(name, filepath.Ext(name))
		}
	}
	respond(w, stage)
}
func (a *App) inspectMapPath(file string) (*Stage, error) {
	if !strings.EqualFold(filepath.Ext(file), ".zip") && !strings.HasPrefix(filepath.Base(file), "upload-") {
		return nil, fmt.Errorf("请选择地图 ZIP 文件")
	}
	if s, e := os.Stat(file); e != nil || s.IsDir() || s.Size() > 2<<30 {
		return nil, fmt.Errorf("地图 ZIP 不存在或超过 2 GB")
	}
	id := newID()
	dir := filepath.Join(a.root, "staging", id, "extracted")
	if e := os.MkdirAll(dir, 0755); e != nil {
		return nil, e
	}
	candidates, e := extractMap(file, dir)
	if e != nil {
		os.RemoveAll(filepath.Dir(dir))
		return nil, e
	}
	stage := &Stage{ID: id, Candidates: candidates}
	a.mu.Lock()
	a.stages[id] = stage
	a.mu.Unlock()
	return stage, nil
}
func (a *App) commit(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in struct {
		Stage       string `json:"stage"`
		Index       int    `json:"index"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	world, e := a.commitStage(in.Stage, in.Index, in.Name, in.Description, in.Version)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, world)
}
func (a *App) cancelImport(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in struct {
		Stage string `json:"stage"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stages[in.Stage] != nil {
		delete(a.stages, in.Stage)
		_ = os.RemoveAll(filepath.Join(a.root, "staging", in.Stage))
	}
	respond(w, map[string]bool{"ok": true})
}
func (a *App) editWorld(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in World
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || len(in.Description) > 4000 || !validVersion.MatchString(in.Version) {
		fail(w, fmt.Errorf("名称、简介或版本格式不正确"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.run != nil && a.run.WorldID == in.ID && (a.run.State == "starting" || a.run.State == "running" || a.run.State == "stopping") {
		fail(w, fmt.Errorf("请先停止这张地图再编辑"))
		return
	}
	for i, old := range a.db.Worlds {
		if old.ID == in.ID {
			in.Added = old.Added
			in.Engine = old.Engine
			a.db.Worlds[i] = in
			if e := a.saveLocked(); e != nil {
				a.db.Worlds[i] = old
				fail(w, e)
				return
			}
			respond(w, in)
			return
		}
	}
	fail(w, fmt.Errorf("地图不存在"))
}
func (a *App) start(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	if e := a.startWorld(in.ID); e != nil {
		fail(w, e)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (a *App) stop(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	if e := a.command("stop"); e != nil {
		fail(w, e)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (a *App) sendCommand(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in struct {
		Command string `json:"command"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	if e := a.command(in.Command); e != nil {
		fail(w, e)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (a *App) icon(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	a.mu.Lock()
	world, e := a.findLocked(id)
	a.mu.Unlock()
	if e != nil {
		http.NotFound(w, r)
		return
	}
	dest := filepath.Join(a.instance(world.ID), "cover")
	if r.Method == "GET" {
		if _, e = os.Stat(dest); os.IsNotExist(e) {
			dest = filepath.Join(a.instance(world.ID), "world", "icon.png")
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, dest)
		return
	}
	if !method(w, r, "POST") {
		return
	}
	file, _, e := a.receiveFile(w, r, 5<<20)
	if e != nil {
		fail(w, e)
		return
	}
	defer os.Remove(file)
	b, e := os.ReadFile(file)
	if e != nil {
		fail(w, e)
		return
	}
	typ := http.DetectContentType(b)
	if typ != "image/png" && typ != "image/jpeg" && typ != "image/webp" {
		fail(w, fmt.Errorf("封面只支持 PNG、JPEG 或 WebP"))
		return
	}
	if e = atomicWrite(dest, b); e != nil {
		fail(w, e)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
func (a *App) versions(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	manifest, e := a.getManifest(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	for i := range manifest.Versions {
		manifest.Versions[i].Installed = a.installed(manifest.Versions[i].ID)
	}
	respond(w, manifest)
}
func (a *App) downloadVersion(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in struct {
		Version string `json:"version"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	task, e := a.startDownload(in.Version)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, task)
}
func (a *App) importJar(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	file, name, e := a.receiveFile(w, r, 512<<20)
	if e != nil {
		fail(w, e)
		return
	}
	defer os.Remove(file)
	if !strings.EqualFold(filepath.Ext(name), ".jar") {
		fail(w, fmt.Errorf("请选择官方服务端 JAR"))
		return
	}
	version, e := jarVersion(file)
	if e != nil {
		fail(w, e)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	dest := a.serverJar(version)
	if !a.installed(version) {
		if e = os.MkdirAll(filepath.Dir(dest), 0755); e == nil {
			e = os.Rename(file, dest)
		}
		if e != nil {
			fail(w, e)
			return
		}
	}
	respond(w, map[string]string{"version": version})
}
func (a *App) backup(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	world, e := a.findLocked(in.ID)
	if e != nil {
		fail(w, e)
		return
	}
	if a.run != nil && a.run.WorldID == in.ID && a.run.State != "stopped" && a.run.State != "failed" {
		fail(w, fmt.Errorf("请先停止地图再备份，以保证存档完整"))
		return
	}
	name := world.ID + "-" + time.Now().Format("20060102-150405") + "-" + newID()[:6] + ".zip"
	dest := filepath.Join(a.root, "backups", name)
	e = zipDirectory(a.instance(world.ID), dest)
	if e != nil {
		os.Remove(dest)
		fail(w, e)
		return
	}
	respond(w, map[string]string{"path": dest})
}
func zipDirectory(root, dest string) error {
	file, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return e
	}
	z := zip.NewWriter(file)
	e = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("备份中不支持符号链接")
		}
		if d.Name() == "session.lock" {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		out, e := z.Create(filepath.ToSlash(rel))
		if e != nil {
			return e
		}
		src, e := os.Open(p)
		if e != nil {
			return e
		}
		defer src.Close()
		_, e = io.Copy(out, src)
		return e
	})
	ze := z.Close()
	fe := file.Close()
	if e != nil {
		return e
	}
	if ze != nil {
		return ze
	}
	return fe
}
