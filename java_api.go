package main

import (
	"fmt"
	"net/http"
	"path/filepath"
)

func (a *App) scanJava(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	a.startJavaScan()
	respond(w, map[string]bool{"ok": true})
}
func (a *App) browseJava(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in struct {
		Kind string `json:"kind"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	if in.Kind != "java" && in.Kind != "directory" {
		fail(w, fmt.Errorf("不支持的文件选择类型"))
		return
	}
	a.mu.Lock()
	pick := a.pickNative
	a.mu.Unlock()
	if pick == nil {
		fail(w, fmt.Errorf("本地文件浏览需要桌面版；网页开发模式可手动配置路径"))
		return
	}
	path, e := pick(in.Kind)
	if e != nil {
		fail(w, e)
		return
	}
	if path == "" {
		respond(w, map[string]bool{"cancelled": true})
		return
	}
	if in.Kind == "java" {
		j, e := inspectJava(path, "手动添加")
		if e != nil {
			fail(w, e)
			return
		}
		a.mu.Lock()
		a.mergeJavaLocked(j)
		root := filepath.Dir(filepath.Dir(j.Path))
		old := a.db.Settings.JavaRoots
		if !containsPath(old, root) {
			a.db.Settings.JavaRoots = append(a.db.Settings.JavaRoots, root)
		}
		e = a.saveLocked()
		if e != nil {
			a.db.Settings.JavaRoots = old
		}
		_ = atomicJSON(filepath.Join(a.root, "java-cache.json"), a.javas)
		a.mu.Unlock()
		if e != nil {
			fail(w, e)
			return
		}
		respond(w, j)
		return
	}
	a.mu.Lock()
	old := a.db.Settings.JavaRoots
	if len(old) >= 32 {
		a.mu.Unlock()
		fail(w, fmt.Errorf("最多添加 32 个搜索目录"))
		return
	}
	if !containsPath(old, path) {
		a.db.Settings.JavaRoots = append(a.db.Settings.JavaRoots, path)
	}
	e = a.saveLocked()
	if e != nil {
		a.db.Settings.JavaRoots = old
	}
	a.mu.Unlock()
	if e != nil {
		fail(w, e)
		return
	}
	a.startJavaScan()
	respond(w, map[string]bool{"ok": true})
}
func containsPath(paths []string, path string) bool {
	for _, p := range paths {
		if filepath.Clean(p) == filepath.Clean(path) {
			return true
		}
	}
	return false
}
func (a *App) javaRequirementAPI(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "GET") {
		return
	}
	version := r.URL.Query().Get("version")
	if !validVersion.MatchString(version) {
		fail(w, fmt.Errorf("版本无效"))
		return
	}
	a.mu.Lock()
	req, e := a.javaRequirement(version)
	if e != nil {
		a.mu.Unlock()
		fail(w, e)
		return
	}
	chosen, choiceErr := chooseJava(a.javas, req)
	a.mu.Unlock()
	respond(w, map[string]any{"requirement": req, "selected": chosen, "missing": choiceErr != nil})
}
