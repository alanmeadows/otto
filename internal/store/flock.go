package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// DefaultLockTimeout is the default timeout for acquiring a file lock.
const DefaultLockTimeout = 5 * time.Second

// WithLock acquires an exclusive lock on path.lock, runs fn, then releases.
func WithLock(path string, timeout time.Duration, fn func() error) error {
	lockPath := path + ".lock"
	// Ensure parent directory exists so the lock file can be created.
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return fmt.Errorf("creating lock directory: %w", err)
	}
	fileLock := flock.New(lockPath)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquiring lock on %s: %w", lockPath, err)
	}
	if !locked {
		return fmt.Errorf("timed out acquiring lock on %s", lockPath)
	}
	defer fileLock.Unlock()

	return fn()
}

// WithReadLock acquires a shared read lock on path.lock, runs fn, then releases.
func WithReadLock(path string, timeout time.Duration, fn func() error) error {
	lockPath := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return fmt.Errorf("creating lock directory: %w", err)
	}
	fileLock := flock.New(lockPath)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	locked, err := fileLock.TryRLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquiring read lock on %s: %w", lockPath, err)
	}
	if !locked {
		return fmt.Errorf("timed out acquiring read lock on %s", lockPath)
	}
	defer fileLock.Unlock()

	return fn()
}
