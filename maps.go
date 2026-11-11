package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var validMapID = regexp.MustCompile(`^[a-f0-9]{24}$`)

// Remove a stopped map from the library without destroying its world files.
func (a *App) deleteWorld(id, confirmName string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	world, e := a.findLocked(id)
	if e != nil {
		return "", e
	}
	if !validMapID.MatchString(world.ID) {
		return "", fmt.Errorf("地图标识无效")
	}
	if world.Name != confirmName {
		return "", fmt.Errorf("地图名称已改变，请重新确认删除")
	}
	if a.run != nil && a.run.WorldID == id && a.run.State != "stopped" && a.run.State != "failed" {
		return "", fmt.Errorf("请先关服并等待保存完成，再删除地图")
	}
	source := a.instance(id)
	trashRoot := filepath.Join(a.root, "trash")
	if e = os.MkdirAll(trashRoot, 0755); e != nil {
		return "", e
	}
	dest := filepath.Join(trashRoot, id+"-"+time.Now().Format("20060102-150405")+"-"+newID()[:6])
	// All move targets are direct descendants of the explicit application data directory.
	for _, target := range []string{source, dest} {
		rel, e := filepath.Rel(a.root, target)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("地图路径不在应用数据目录中")
		}
	}
	if e = os.Rename(source, dest); e != nil {
		return "", fmt.Errorf("无法移动地图文件：%w", e)
	}
	metadata := map[string]any{"map": world, "deletedAt": time.Now().Format(time.RFC3339)}
	if e = atomicJSON(filepath.Join(dest, "deleted-map.json"), metadata); e != nil {
		_ = os.Rename(dest, source)
		return "", e
	}
	old := a.db.Worlds
	next := make([]World, 0, len(old)-1)
	for _, w := range old {
		if w.ID != id {
			next = append(next, w)
		}
	}
	a.db.Worlds = next
	if e = a.saveLocked(); e != nil {
		a.db.Worlds = old
		_ = os.Rename(dest, source)
		return "", e
	}
	if a.run != nil && a.run.WorldID == id {
		a.run = nil
		a.logs = []string{}
	}
	return dest, nil
}
func (a *App) deleteWorldAPI(w http.ResponseWriter, r *http.Request) {
	if !method(w, r, "POST") {
		return
	}
	var in struct {
		ID   string `json:"id"`
		Name string `json:"confirmName"`
	}
	if e := decode(w, r, &in); e != nil {
		fail(w, e)
		return
	}
	dest, e := a.deleteWorld(in.ID, in.Name)
	if e != nil {
		fail(w, e)
		return
	}
	respond(w, map[string]string{"trashPath": dest})
}
