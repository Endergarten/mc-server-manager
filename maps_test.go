package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteMapPreservesWorldProgress(t *testing.T) {
	a := testApp(t)
	world := World{ID: newID(), Name: "My map", Version: "26.3"}
	a.db.Worlds = append(a.db.Worlds, world)
	progress := filepath.Join(a.instance(world.ID), "world", "playerdata", "player.dat")
	os.MkdirAll(filepath.Dir(progress), 0755)
	os.WriteFile(progress, []byte("saved inventory and progress"), 0644)
	if _, e := a.deleteWorld(world.ID, "wrong name"); e == nil {
		t.Fatal("missing confirmation accepted")
	}
	a.run = &Run{WorldID: world.ID, State: "running"}
	if _, e := a.deleteWorld(world.ID, world.Name); e == nil {
		t.Fatal("running map deleted")
	}
	a.run.State = "stopped"
	dest, e := a.deleteWorld(world.ID, world.Name)
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dest, "world", "playerdata", "player.dat"))
	if e != nil || string(b) != "saved inventory and progress" {
		t.Fatal("saved progress was not preserved")
	}
	if len(a.db.Worlds) != 0 {
		t.Fatal("map retained in library")
	}
	if _, e := os.Stat(a.instance(world.ID)); !os.IsNotExist(e) {
		t.Fatal("old map path still exists")
	}
	loaded, e := newApp(a.root)
	if e != nil || len(loaded.db.Worlds) != 0 {
		t.Fatal("deletion was not persisted")
	}
}
