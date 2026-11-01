package store

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// LockProcess prevents two service instances from recovering/executing the same
// journal at once. It is released by the OS on exit, including crashes. All
// instances controlling the same device must share this DB and lock; separate
// databases cannot coordinate physical ownership.
func LockProcess(databasePath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, err
	}
	// Resolve existing symlinks so aliases of a database share the same lock.
	if resolved, err := filepath.EvalSymlinks(databasePath); err == nil {
		databasePath = resolved
	} else if parent, err := filepath.EvalSymlinks(filepath.Dir(databasePath)); err == nil {
		databasePath = filepath.Join(parent, filepath.Base(databasePath))
	}
	f, err := os.OpenFile(databasePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another driver instance owns this database: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
