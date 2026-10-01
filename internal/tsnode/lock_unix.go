// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package tsnode

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const lockFileName = ".trellis.lock"

// lockStateDir creates dir (mode 0700) if needed and takes a non-blocking
// exclusive flock on dir/.trellis.lock. The lock is per open file
// description, so it also excludes other registries in this process.
func lockStateDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file in %s: %w", dir, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("state directory %s is in use by another process (lock held on %s)", dir, lockFileName)
		}
		return nil, fmt.Errorf("lock state directory %s: %w", dir, err)
	}
	return f, nil
}

func unlockStateDir(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
