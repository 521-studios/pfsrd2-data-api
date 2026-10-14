package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSetGlobalSwapsAndRetiresOldHandle(t *testing.T) {
	saved := retireGrace
	retireGrace = 20 * time.Millisecond
	t.Cleanup(func() { retireGrace = saved })

	dir := t.TempDir()
	first, err := Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(filepath.Join(dir, "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	prev := Global()
	t.Cleanup(func() { SetGlobal(prev) })

	SetGlobal(first)
	SetGlobal(second)
	if Global() != second {
		t.Fatal("Global() did not return the newest handle")
	}
	// Within the grace period a request holding the old handle still works.
	if _, err := first.Exec("SELECT 1"); err != nil {
		t.Fatalf("old handle unusable inside grace: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		if _, err := first.Exec("SELECT 1"); err != nil {
			break // closed
		}
		if time.Now().After(deadline) {
			t.Fatal("old handle never closed after retireGrace")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
