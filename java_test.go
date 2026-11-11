package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeJava(t *testing.T, version, arch string) JavaRuntime {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "bin", javaExecutable())
	os.MkdirAll(filepath.Dir(p), 0755)
	os.WriteFile(p, []byte("placeholder"), 0755)
	os.WriteFile(filepath.Join(root, "release"), []byte("JAVA_VERSION=\""+version+"\"\nOS_ARCH=\""+arch+"\"\nIMPLEMENTOR=\"Test JDK\""), 0644)
	j, e := inspectJava(p, "test")
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func TestJavaVersionAndArchitecture(t *testing.T) {
	for version, want := range map[string]int{"1.8.0_411": 8, "17.0.15": 17, "21.0.8+9": 21, "25.0.1": 25} {
		if got := javaMajor(version); got != want {
			t.Fatalf("%s: %d", version, got)
		}
	}
	j := fakeJava(t, "25.0.1", "x86_64")
	if j.Major != 25 || j.Arch != "amd64" || j.Vendor != "Test JDK" {
		t.Fatalf("%+v", j)
	}
}
func TestAutomaticJavaUsesCorrectMajor(t *testing.T) {
	arch := runtime.GOARCH
	j17 := fakeJava(t, "17.0.15", arch)
	j21 := fakeJava(t, "21.0.3", arch)
	j25 := fakeJava(t, "25.0.1", arch)
	new21 := fakeJava(t, "21.0.9", arch)
	for _, major := range []int{17, 21, 25} {
		chosen, e := chooseJava([]JavaRuntime{j25, j21, j17, new21}, JavaRequirement{Preferred: major, Allowed: []int{major}})
		if e != nil || chosen.Major != major {
			t.Fatalf("wrong Java for %d: %+v %v", major, chosen, e)
		}
		if major == 21 && chosen.Version != "21.0.9" {
			t.Fatal("newest patch not selected")
		}
	}
	if _, e := chooseJava([]JavaRuntime{j25}, JavaRequirement{Preferred: 8, Allowed: []int{8}}); e == nil {
		t.Fatal("incompatible Java accepted")
	}
	if runtime.GOARCH == "amd64" {
		j32 := fakeJava(t, "21.0.9", "x86")
		if _, e := chooseJava([]JavaRuntime{j32}, JavaRequirement{Preferred: 21, Allowed: []int{21}}); e == nil {
			t.Fatal("32-bit Java selected")
		}
	}
}
func TestJavaRequirementsOfficialAndOverrides(t *testing.T) {
	a := testApp(t)
	for v, major := range map[string]int{"1.16.5": 8, "1.17.1": 16, "1.18.2": 17, "1.20.4": 17, "1.20.5": 21, "26.3": 25} {
		req, e := a.javaRequirement(v)
		if e != nil || req.Preferred != major {
			t.Fatalf("%s: %+v %v", v, req, e)
		}
	}
	version := "27w01a"
	dir := filepath.Dir(a.serverJar(version))
	os.MkdirAll(dir, 0755)
	meta := VersionMeta{ID: version}
	meta.JavaVersion.Major = 26
	atomicJSON(filepath.Join(dir, "metadata.json"), meta)
	req, e := a.javaRequirement(version)
	if e != nil || req.Preferred != 26 || req.Source != "Mojang 官方版本信息" {
		t.Fatalf("official requirement lost: %+v %v", req, e)
	}
	custom := a.javaRules
	custom.Overrides = []JavaRule{{Versions: []string{version}, Preferred: 25, Allowed: []int{25, 26}}}
	atomicJSON(filepath.Join(a.root, "java-rules.json"), custom)
	req, e = a.javaRequirement(version)
	if e != nil || req.Preferred != 25 || len(req.Allowed) != 2 {
		t.Fatal("override ignored")
	}
	if _, e = a.javaRequirement("unknown-snapshot"); e == nil {
		t.Fatal("unknown version guessed")
	}
}
func TestNestedServerJavaVersion(t *testing.T) {
	inner := zipBytes(t, map[string][]byte{"version.json": []byte(`{"id":"26.3","java_version":25}`)})
	outer := writeArchive(t, zipBytes(t, map[string][]byte{"META-INF/versions/26.3/server.jar": inner}))
	if major := javaMajorInJar(outer); major != 25 {
		t.Fatalf("nested metadata: %d", major)
	}
}
func TestNativeJavaPickerAndCancellation(t *testing.T) {
	a := testApp(t)
	j := fakeJava(t, "21.0.9", runtime.GOARCH)
	a.pickNative = func(kind string) (string, error) {
		if kind != "java" {
			t.Fatalf("unexpected kind %s", kind)
		}
		return j.Path, nil
	}
	r := httptest.NewRequest(http.MethodPost, "/api/java/browse", strings.NewReader(`{"kind":"java"}`))
	w := httptest.NewRecorder()
	a.browseJava(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(a.javas) == 0 || !containsPath(a.db.Settings.JavaRoots, filepath.Dir(filepath.Dir(j.Path))) {
		t.Fatal("selected Java not remembered")
	}
	a.pickNative = func(string) (string, error) { return "", nil }
	r = httptest.NewRequest(http.MethodPost, "/api/java/browse", strings.NewReader(`{"kind":"java"}`))
	w = httptest.NewRecorder()
	a.browseJava(w, r)
	if !bytes.Contains(w.Body.Bytes(), []byte(`"cancelled":true`)) {
		t.Fatal("cancel not handled")
	}
	var saved Database
	b, _ := os.ReadFile(filepath.Join(a.root, "library.json"))
	if e := json.Unmarshal(b, &saved); e != nil || len(saved.Settings.JavaRoots) == 0 {
		t.Fatal("discovery root not persisted")
	}
}
func TestJavaRulesValidate(t *testing.T) {
	if validateJavaRules(JavaRules{Overrides: []JavaRule{{Preferred: 25, Allowed: []int{21}}}}) == nil {
		t.Fatal("invalid preferred accepted")
	}
}
