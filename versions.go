package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const manifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"

type Version struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	Installed bool   `json:"installed"`
}
type Manifest struct {
	Versions []Version `json:"versions"`
}
type Artifact struct {
	URL  string `json:"url"`
	SHA1 string `json:"sha1"`
	Size int64  `json:"size"`
}
type VersionMeta struct {
	ID          string              `json:"id"`
	Downloads   map[string]Artifact `json:"downloads"`
	JavaVersion struct {
		Major int `json:"majorVersion"`
	} `json:"javaVersion"`
}
type Task struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	State   string `json:"state"`
	Bytes   int64  `json:"bytes"`
	Total   int64  `json:"total"`
	Error   string `json:"error,omitempty"`
}

var httpClient = &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) > 5 {
		return fmt.Errorf("重定向过多")
	}
	return trustedURL(req.URL.String())
}}

func trustedURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil {
		return fmt.Errorf("下载地址无效")
	}
	switch u.Host {
	case "piston-meta.mojang.com", "piston-data.mojang.com", "launchermeta.mojang.com", "launcher.mojang.com":
		return nil
	}
	return fmt.Errorf("只允许 Mojang 官方下载地址")
}
func fetchJSON(ctx context.Context, address string, out any) error {
	if e := trustedURL(address); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "MC-Server-Manager/0.1")
	resp, e := httpClient.Do(req)
	if e != nil {
		return fmt.Errorf("无法连接 Mojang 官方服务：%w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("官方服务返回 %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}
func (a *App) getManifest(ctx context.Context) (Manifest, error) {
	a.mu.Lock()
	if a.manifest != nil && time.Since(a.manifestAt) < 15*time.Minute {
		m := *a.manifest
		m.Versions = append([]Version{}, m.Versions...)
		a.mu.Unlock()
		return m, nil
	}
	a.mu.Unlock()
	var m Manifest
	if e := fetchJSON(ctx, manifestURL, &m); e != nil {
		return m, e
	}
	a.mu.Lock()
	a.manifest = &m
	a.manifestAt = time.Now()
	a.mu.Unlock()
	m.Versions = append([]Version{}, m.Versions...)
	return m, nil
}
func (a *App) versionMeta(ctx context.Context, id string) (VersionMeta, error) {
	var meta VersionMeta
	m, e := a.getManifest(ctx)
	if e != nil {
		return meta, e
	}
	for _, v := range m.Versions {
		if v.ID == id {
			e = fetchJSON(ctx, v.URL, &meta)
			return meta, e
		}
	}
	return meta, fmt.Errorf("官方列表没有版本 %s，请检查是否区分了正式版和候选版", id)
}
func (a *App) serverJar(version string) string {
	return filepath.Join(a.root, "servers", version, "server.jar")
}
func (a *App) installed(version string) bool {
	if !validVersion.MatchString(version) {
		return false
	}
	s, e := os.Stat(a.serverJar(version))
	return e == nil && s.Size() > 0
}
func (a *App) startDownload(version string) (Task, error) {
	if !validVersion.MatchString(version) {
		return Task{}, fmt.Errorf("版本格式不正确")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.installed(version) {
		return Task{Version: version, State: "done"}, nil
	}
	for _, t := range a.tasks {
		if t.Version == version && (t.State == "queued" || t.State == "downloading") {
			return *t, nil
		}
	}
	t := &Task{ID: newID(), Version: version, State: "queued"}
	a.tasks[t.ID] = t
	go a.download(t.ID, version)
	return *t, nil
}
func (a *App) taskUpdate(id string, fn func(*Task)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn(a.tasks[id])
}

type progressWriter struct {
	a    *App
	id   string
	last time.Time
	n    int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if time.Since(p.last) > 200*time.Millisecond {
		p.last = time.Now()
		p.a.taskUpdate(p.id, func(t *Task) { t.Bytes = p.n })
	}
	return len(b), nil
}
func (a *App) download(id, version string) {
	e := a.downloadFile(id, version)
	a.taskUpdate(id, func(t *Task) {
		if e != nil {
			t.State = "failed"
			t.Error = e.Error()
		} else {
			t.State = "done"
			t.Bytes = t.Total
		}
	})
}
func (a *App) downloadFile(id, version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	meta, e := a.versionMeta(ctx, version)
	if e != nil {
		return e
	}
	artifact, ok := meta.Downloads["server"]
	if !ok {
		return fmt.Errorf("该版本没有官方 Java 服务端下载")
	}
	if e = trustedURL(artifact.URL); e != nil {
		return e
	}
	expected, e := hex.DecodeString(artifact.SHA1)
	if e != nil || len(expected) != 20 || artifact.Size <= 0 || artifact.Size > 512<<20 {
		return fmt.Errorf("官方下载信息异常")
	}
	dest := a.serverJar(version)
	if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
		return e
	}
	temp, e := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if e != nil {
		return e
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	a.taskUpdate(id, func(t *Task) { t.State = "downloading"; t.Total = artifact.Size })
	req, e := http.NewRequestWithContext(ctx, "GET", artifact.URL, nil)
	if e != nil {
		return e
	}
	resp, e := httpClient.Do(req)
	if e != nil {
		return fmt.Errorf("下载失败：%w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载服务返回 %d", resp.StatusCode)
	}
	h := sha1.New()
	pw := &progressWriter{a: a, id: id}
	n, e := io.Copy(io.MultiWriter(temp, h, pw), io.LimitReader(resp.Body, artifact.Size+1))
	if e != nil {
		return e
	}
	if n != artifact.Size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), artifact.SHA1) {
		return fmt.Errorf("文件大小或 SHA-1 校验不符，请重新下载")
	}
	actual, e := jarVersion(temp.Name())
	if e != nil && !errors.Is(e, errJarNoVersion) {
		return e
	}
	if e == nil && actual != version {
		return fmt.Errorf("JAR 内部版本与所选版本不符")
	}
	if e = temp.Sync(); e != nil {
		return e
	}
	if e = temp.Close(); e != nil {
		return e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.installed(version) {
		return nil
	}
	if e = atomicJSON(filepath.Join(filepath.Dir(dest), "metadata.json"), meta); e != nil {
		return e
	}
	if e = os.Rename(temp.Name(), dest); e != nil {
		return e
	}
	return nil
}

// Older official jars do not embed a version. For those, verify the recorded
// Mojang artifact hash instead of guessing from the filename.
func (a *App) verifyInstalled(version string) error {
	actual, e := jarVersion(a.serverJar(version))
	if e == nil {
		if actual != version {
			return fmt.Errorf("服务端内部版本与地图要求不一致")
		}
		return nil
	}
	if !errors.Is(e, errJarNoVersion) {
		return e
	}
	b, e := os.ReadFile(filepath.Join(filepath.Dir(a.serverJar(version)), "metadata.json"))
	if e != nil {
		return fmt.Errorf("旧版服务端缺少官方校验信息，请重新从版本列表下载")
	}
	var meta VersionMeta
	if e = json.Unmarshal(b, &meta); e != nil {
		return e
	}
	artifact, ok := meta.Downloads["server"]
	if !ok || meta.ID != version || trustedURL(artifact.URL) != nil {
		return fmt.Errorf("旧版服务端校验信息无效")
	}
	f, e := os.Open(a.serverJar(version))
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha1.New()
	n, e := io.Copy(h, f)
	if e != nil {
		return e
	}
	if n != artifact.Size || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), artifact.SHA1) {
		return fmt.Errorf("旧版服务端校验失败，请重新下载")
	}
	return nil
}
