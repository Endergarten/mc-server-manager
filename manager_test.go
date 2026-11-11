package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("MC_MANAGER_TEST_SERVER") == "1" {
		fmt.Println("Done (0.1s)! For help, type help")
		s := bufio.NewScanner(os.Stdin)
		for s.Scan() {
			if s.Text() == "stop" {
				fmt.Println("Saving worlds: complete")
				os.Exit(0)
			}
			fmt.Println("COMMAND: " + s.Text())
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}
func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, data := range files {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func writeArchive(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "map.zip")
	if e := os.WriteFile(p, b, 0644); e != nil {
		t.Fatal(e)
	}
	return p
}
func testApp(t *testing.T) *App {
	t.Helper()
	a, e := newApp(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func TestArchivePathRejection(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "C:/file", "world/../../evil", "world\\..\\..\\evil", "world/NUL.txt", "world/test.", "world/file:stream"} {
		t.Run(name, func(t *testing.T) {
			if _, e := safeArchivePath(name); e == nil {
				t.Fatal("unsafe path accepted")
			}
		})
	}
}
func TestImportWrappedMultipleAndPersist(t *testing.T) {
	a := testApp(t)
	id := newID()
	dir := filepath.Join(a.root, "staging", id, "extracted")
	os.MkdirAll(dir, 0755)
	archive := writeArchive(t, zipBytes(t, map[string][]byte{"bundle/Map-A/level.dat": {1}, "bundle/Map-A/mcversion.txt": []byte("26.3"), "bundle/Map-A/icon.png": {1, 2}, "bundle/Map-B/level.dat": {1}}))
	candidates, e := extractMap(archive, dir)
	if e != nil {
		t.Fatal(e)
	}
	if len(candidates) != 2 || candidates[0].Version != "26.3" || !candidates[0].HasIcon {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
	a.stages[id] = &Stage{ID: id, Candidates: candidates}
	w, e := a.commitStage(id, 0, "Test server", "A map", "26.3")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(a.instance(w.ID), "world", "level.dat")); e != nil {
		t.Fatal(e)
	}
	loaded, e := newApp(a.root)
	if e != nil {
		t.Fatal(e)
	}
	if len(loaded.db.Worlds) != 1 || loaded.db.Worlds[0].Description != "A map" {
		t.Fatal("library not persisted")
	}
	if _, e = a.commitStage(id, 0, "again", "", "26.3"); e == nil {
		t.Fatal("stage reused")
	}
}
func TestZipRejectTraversalAndDuplicateCase(t *testing.T) {
	for _, files := range []map[string][]byte{{"../escape": {1}}, {"World/level.dat": {1}, "world/level.dat": {2}}} {
		p := writeArchive(t, zipBytes(t, files))
		if _, e := extractMap(p, t.TempDir()); e == nil {
			t.Fatal("bad archive accepted")
		}
	}
}
func TestWorldVersionNBT(t *testing.T) {
	dir := t.TempDir()
	var b bytes.Buffer
	str := func(s string) { binary.Write(&b, binary.BigEndian, uint16(len(s))); b.WriteString(s) }
	b.WriteByte(10)
	str("")
	b.WriteByte(10)
	str("Data")
	b.WriteByte(8)
	str("LevelName")
	str("§aMiniGames")
	b.WriteByte(10)
	str("Version")
	b.WriteByte(8)
	str("Name")
	str("26.3 Release Candidate 3")
	b.Write([]byte{0, 0, 0})
	file, e := os.Create(filepath.Join(dir, "level.dat"))
	if e != nil {
		t.Fatal(e)
	}
	g := gzip.NewWriter(file)
	g.Write(b.Bytes())
	g.Close()
	file.Close()
	version, _, name := detectWorld(dir)
	if version != "26.3-rc-3" || stripFormatting(name) != "MiniGames" {
		t.Fatalf("%s %s", version, name)
	}
	os.WriteFile(filepath.Join(dir, "mcversion.txt"), []byte("26.3"), 0644)
	version, source, _ := detectWorld(dir)
	if version != "26.3" || source != "mcversion.txt" {
		t.Fatal("explicit version should win")
	}
}
func TestDefaultPropertiesAndOperator(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "server.properties"), []byte("white-list=true\nmotd=Keep me\nonline-mode=true\n"), 0644)
	s := Settings{Java: "java", Memory: 2, Port: 25565, Operators: []string{"Notch", "TestAdmin"}, EULA: true}
	if e := prepareServer(dir, s); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "server.properties"))
	for _, line := range []string{"white-list=false", "online-mode=false", "allow-flight=true", "enable-command-block=true", "motd=Keep me"} {
		if !strings.Contains(string(b), line) {
			t.Fatalf("missing %s", line)
		}
	}
	if e := prepareServer(dir, s); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "ops.json"))
	var ops []map[string]any
	json.Unmarshal(b, &ops)
	if len(ops) != 2 || ops[0]["uuid"] != "b50ad385-829d-3141-a216-7e7d7539ba7f" || ops[0]["level"] != float64(4) {
		t.Fatalf("wrong op: %s", b)
	}
}
func TestLocalGuard(t *testing.T) {
	a := testApp(t)
	a.address = "http://127.0.0.1:18180"
	a.token = "secret"
	h := a.guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		host, origin, token string
		want                int
	}{{"evil.example", "", "secret", 403}, {"127.0.0.1:18180", "https://evil.example", "secret", 403}, {"127.0.0.1:18180", "", "", 403}, {"127.0.0.1:18180", "http://127.0.0.1:18180", "secret", 204}} {
		r := httptest.NewRequest("POST", a.address+"/api/settings", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Manager-Token", tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d", tc, w.Code)
		}
	}
}
func installFakeJar(t *testing.T, a *App, version string) {
	t.Helper()
	p := a.serverJar(version)
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, zipBytes(t, map[string][]byte{"META-INF/versions.list": []byte("hash\t" + version + "\t" + version + "/server.jar\n")}), 0644)
}
func TestServerLifecycleCommandAndStop(t *testing.T) {
	a := testApp(t)
	exe, _ := os.Executable()
	probe, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	a.db.Settings = Settings{Java: exe, Memory: 1, Port: port, Operators: []string{"Notch", "TestAdmin"}, EULA: true}
	world := World{ID: newID(), Name: "Test", Version: "26.3"}
	a.db.Worlds = append(a.db.Worlds, world)
	os.MkdirAll(filepath.Join(a.instance(world.ID), "world"), 0755)
	os.WriteFile(filepath.Join(a.instance(world.ID), "world", "level.dat"), []byte{1}, 0644)
	installFakeJar(t, a, "26.3")
	t.Setenv("MC_MANAGER_TEST_SERVER", "1")
	if e = a.startWorld(world.ID); e != nil {
		t.Fatal(e)
	}
	defer a.stopAndWait(3 * time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		s := a.run.State
		a.mu.Unlock()
		if s == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = a.startWorld(world.ID); e == nil {
		t.Fatal("concurrent start allowed")
	}
	if e = a.command("op TestAdmin"); e != nil {
		t.Fatal(e)
	}
	if e = a.command("say hello\nstop"); e == nil {
		t.Fatal("multiline command accepted")
	}
	if e = a.stopAndWait(5 * time.Second); e != nil {
		t.Fatal(e)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.run.State != "stopped" || !strings.Contains(strings.Join(a.logs, "\n"), "Saving worlds: complete") {
		t.Fatalf("not gracefully stopped: %v", a.logs)
	}
}

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOfficialDownloadAndCorruption(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			a := testApp(t)
			artifact := zipBytes(t, map[string][]byte{"META-INF/versions.list": []byte("hash\t26.3\t26.3/server.jar")})
			sum := sha1.Sum(artifact)
			meta := VersionMeta{ID: "26.3", Downloads: map[string]Artifact{"server": {URL: "https://piston-data.mojang.com/server.jar", SHA1: hex.EncodeToString(sum[:]), Size: int64(len(artifact))}}}
			a.manifest = &Manifest{Versions: []Version{{ID: "26.3", URL: "https://piston-meta.mojang.com/version.json"}}}
			a.manifestAt = time.Now()
			old := httpClient
			defer func() { httpClient = old }()
			httpClient = &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
				var data []byte
				if strings.HasSuffix(r.URL.Path, "version.json") {
					data, _ = json.Marshal(meta)
				} else {
					data = append([]byte{}, artifact...)
					if corrupt {
						data[0] ^= 1
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
			})}
			task := &Task{ID: "test", Version: "26.3"}
			a.tasks[task.ID] = task
			e := a.downloadFile(task.ID, "26.3")
			if corrupt {
				if e == nil || a.installed("26.3") {
					t.Fatal("corrupt download installed")
				}
			} else {
				if e != nil || !a.installed("26.3") {
					t.Fatalf("download failed: %v", e)
				}
			}
		})
	}
}
func TestUntrustedDownloadURL(t *testing.T) {
	for _, url := range []string{"http://piston-data.mojang.com/a", "https://evil.example/a", "https://piston-data.mojang.com.evil.example/a", "https://user@piston-data.mojang.com/a"} {
		if trustedURL(url) == nil {
			t.Fatal("bad URL accepted")
		}
	}
	a := testApp(t)
	a.manifest = &Manifest{}
	a.manifestAt = time.Now()
	if _, e := a.versionMeta(context.Background(), "unknown"); e == nil {
		t.Fatal("unknown version accepted")
	}
}

func TestAdminListValidationAndSync(t *testing.T) {
	a := testApp(t)
	if len(a.db.Settings.Operators) != 0 {
		t.Fatal("fresh installation must not contain a personal account")
	}
	s := a.db.Settings
	s.Operators = []string{"AdminA", "AdminB"}
	if e := validateSettings(s); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e := prepareServer(dir, s); e != nil {
		t.Fatal(e)
	}
	s.Operators = []string{}
	if e := prepareServer(dir, s); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "ops.json"))
	var ops []any
	json.Unmarshal(b, &ops)
	if len(ops) != 0 {
		t.Fatal("removed admins retained")
	}
	if _, e := os.Stat(filepath.Join(dir, "ops.before-manager.json")); e != nil {
		t.Fatal("previous list not backed up")
	}
	for _, names := range [][]string{{"bad name"}, {"AdminA", "admina"}, {"name\nstop"}} {
		s.Operators = names
		if validateSettings(s) == nil {
			t.Fatal("invalid list accepted")
		}
	}
}
func TestSingleAdminMigration(t *testing.T) {
	dir := t.TempDir()
	db := map[string]any{"worlds": []any{}, "settings": map[string]any{"java": "java", "memory": 2, "port": 25565, "operator": "LegacyAdmin", "eula": false}}
	if e := atomicJSON(filepath.Join(dir, "library.json"), db); e != nil {
		t.Fatal(e)
	}
	a, e := newApp(dir)
	if e != nil {
		t.Fatal(e)
	}
	if len(a.db.Settings.Operators) != 1 || a.db.Settings.Operators[0] != "LegacyAdmin" {
		t.Fatal("legacy admin not migrated")
	}
	if e = a.saveLocked(); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "library.json"))
	if bytes.Contains(b, []byte(`"operator":`)) {
		t.Fatal("legacy field retained")
	}
}
func TestLegacyOfficialArtifactVerification(t *testing.T) {
	a := testApp(t)
	version := "1.7.10"
	data := zipBytes(t, map[string][]byte{"META-INF/MANIFEST.MF": []byte("Main-Class: net.minecraft.server.MinecraftServer")})
	p := a.serverJar(version)
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, data, 0644)
	sum := sha1.Sum(data)
	meta := VersionMeta{ID: version, Downloads: map[string]Artifact{"server": {URL: "https://launcher.mojang.com/server.jar", SHA1: hex.EncodeToString(sum[:]), Size: int64(len(data))}}}
	atomicJSON(filepath.Join(filepath.Dir(p), "metadata.json"), meta)
	if e := a.verifyInstalled(version); e != nil {
		t.Fatal(e)
	}
	data[0] ^= 1
	os.WriteFile(p, data, 0644)
	if a.verifyInstalled(version) == nil {
		t.Fatal("modified legacy artifact accepted")
	}
}
