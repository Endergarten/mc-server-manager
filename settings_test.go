package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigurableServerOptions(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			dir := t.TempDir()
			options := &ServerOptions{OnlineMode: mask&1 != 0, CommandBlocks: mask&2 != 0, AllowFlight: mask&4 != 0}
			settings := Settings{Java: "java", Memory: 2, Port: 25565, Operators: []string{}, ServerOptions: options}
			os.WriteFile(filepath.Join(dir, "server.properties"), []byte("white-list=true\nenforce-whitelist=true\nmotd=My map\n"), 0644)
			if e := prepareServer(dir, settings); e != nil {
				t.Fatal(e)
			}
			b, _ := os.ReadFile(filepath.Join(dir, "server.properties"))
			text := string(b)
			for _, line := range []string{fmt.Sprintf("online-mode=%t", options.OnlineMode), fmt.Sprintf("enable-command-block=%t", options.CommandBlocks), fmt.Sprintf("allow-flight=%t", options.AllowFlight), "white-list=false", "enforce-whitelist=false", "motd=My map"} {
				if !strings.Contains(text, line) {
					t.Fatalf("missing %s", line)
				}
			}
		})
	}
}
func TestServerOptionsSaveAndMigration(t *testing.T) {
	a := testApp(t)
	defaults := a.db.Settings.launchOptions()
	if defaults.OnlineMode || !defaults.CommandBlocks || !defaults.AllowFlight {
		t.Fatal("defaults changed")
	}
	s := a.db.Settings
	s.ServerOptions = &ServerOptions{OnlineMode: true, CommandBlocks: false, AllowFlight: false}
	b, _ := json.Marshal(s)
	w := httptest.NewRecorder()
	a.settings(w, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(string(b))))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	loaded, e := newApp(a.root)
	if e != nil {
		t.Fatal(e)
	}
	actual := loaded.db.Settings.launchOptions()
	if !actual.OnlineMode || actual.CommandBlocks || actual.AllowFlight {
		t.Fatal("switch values not persisted")
	}
	s.ServerOptions = nil
	b, _ = json.Marshal(s)
	w = httptest.NewRecorder()
	a.settings(w, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(string(b))))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if a.db.Settings.launchOptions() != defaults {
		t.Fatal("legacy settings did not retain defaults")
	}
}
func TestOnlineOperatorsUseServerIdentityResolution(t *testing.T) {
	a := testApp(t)
	exe, _ := os.Executable()
	a.db.Settings = Settings{Java: exe, Memory: 1, Port: 25579, Operators: []string{"TestAdmin"}, EULA: true, ServerOptions: &ServerOptions{OnlineMode: true, CommandBlocks: true, AllowFlight: true}}
	world := World{ID: newID(), Name: "Online map", Version: "26.3"}
	a.db.Worlds = []World{world}
	os.MkdirAll(filepath.Join(a.instance(world.ID), "world"), 0755)
	os.WriteFile(filepath.Join(a.instance(world.ID), "world", "level.dat"), []byte{1}, 0644)
	installFakeJar(t, a, "26.3")
	t.Setenv("MC_MANAGER_TEST_SERVER", "1")
	if e := a.startWorld(world.ID); e != nil {
		t.Fatal(e)
	}
	defer a.stopAndWait(3 * time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		logs := strings.Join(a.logs, "\n")
		a.mu.Unlock()
		if strings.Contains(logs, "COMMAND: op TestAdmin") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("online op command not sent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(a.instance(world.ID), "ops.json"))
	var ops []any
	json.Unmarshal(b, &ops)
	if len(ops) != 0 {
		t.Fatal("offline identities written in online mode")
	}
	if e := a.stopAndWait(3 * time.Second); e != nil {
		t.Fatal(e)
	}
}
