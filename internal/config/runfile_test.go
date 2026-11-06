// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunFileLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := WriteRunFile("https://box.ts.net:1234", "trellis.hjson"); err != nil {
		t.Fatal(err)
	}
	// A file left by a server that died without cleaning up.
	stale := filepath.Join(home, ".trellis", "run", "999999999.json")
	if err := os.WriteFile(stale, []byte(`{"pid":999999999,"url":"http://gone:1"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	servers := LiveServers()
	if len(servers) != 1 || servers[0].URL != "https://box.ts.net:1234" || servers[0].PID != os.Getpid() {
		t.Fatalf("LiveServers() = %+v, want just this process", servers)
	}
	if !filepath.IsAbs(servers[0].Config) {
		t.Errorf("Config = %q, want an absolute path", servers[0].Config)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale run file was not removed")
	}

	RemoveRunFile()
	if got := LiveServers(); len(got) != 0 {
		t.Fatalf("after RemoveRunFile, LiveServers() = %+v", got)
	}
}
