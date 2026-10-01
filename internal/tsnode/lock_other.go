// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build !unix

package tsnode

import (
	"errors"
	"os"
)

func lockStateDir(dir string) (*os.File, error) {
	return nil, errors.New("tailscale nodes are only supported on unix systems")
}

func unlockStateDir(f *os.File) error { return f.Close() }
