package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcessLockExcludesSecondInstanceAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	unlock, err := LockProcess(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := LockProcess(path); err == nil {
		second()
		t.Fatal("second instance acquired lock")
	}
	unlock()
	second, err := LockProcess(path)
	if err != nil {
		t.Fatalf("lock did not release: %v", err)
	}
	second()
}

func TestProcessLockFollowsDatabaseSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "requests.db")
	alias := filepath.Join(dir, "alias.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	unlock, err := LockProcess(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if second, err := LockProcess(alias); err == nil {
		second()
		t.Fatal("database alias bypassed process lock")
	}
}
