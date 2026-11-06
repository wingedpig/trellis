// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"time"
)

// RunInfo records a running Trellis server so trellis-ctl can find it from a
// directory with no trellis.hjson above it and no TRELLIS_API in the
// environment (e.g. a shell or agent session started outside Trellis).
type RunInfo struct {
	PID     int       `json:"pid"`
	URL     string    `json:"url"`
	Config  string    `json:"config"`
	Started time.Time `json:"started"`
}

// runDir is where servers record themselves: ~/.trellis/run/<pid>.json.
func runDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".trellis", "run"), nil
}

// WriteRunFile records this process as a Trellis server reachable at url.
func WriteRunFile(url, configPath string) error {
	dir, err := runDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}
	data, err := json.MarshalIndent(RunInfo{
		PID: os.Getpid(), URL: url, Config: configPath, Started: time.Now(),
	}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, strconv.Itoa(os.Getpid())+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemoveRunFile removes this process's run file.
func RemoveRunFile() {
	if dir, err := runDir(); err == nil {
		_ = os.Remove(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"))
	}
}

// LiveServers returns the Trellis servers on this machine whose process is
// still running, newest first. Files left behind by a crashed server are
// removed as they are found.
func LiveServers() []RunInfo {
	dir, err := runDir()
	if err != nil {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []RunInfo
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var info RunInfo
		if json.Unmarshal(data, &info) != nil || info.PID <= 0 || info.URL == "" {
			continue
		}
		if !processAlive(info.PID) {
			_ = os.Remove(f)
			continue
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
