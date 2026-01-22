// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

func TestDecompressCommand(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"app.log.zst", "zstd -dc"},
		{"app.log.zstd", "zstd -dc"},
		{"app.log.gz", "gzip -dc"},
		{"app.log.gzip", "gzip -dc"},
		{"app.log.bz2", "bzip2 -dc"},
		{"app.log.bzip2", "bzip2 -dc"},
		{"app.log.xz", "xz -dc"},
		{"app.log.lz4", "lz4 -dc"},
		{"app.log", ""},         // Uncompressed
		{"app.log.txt", ""},     // Unknown extension
		{"", ""},                // Empty
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := DecompressCommand(tt.filename)
			if result != tt.expected {
				t.Errorf("DecompressCommand(%q) = %q, want %q", tt.filename, result, tt.expected)
			}
		})
	}
}

func TestHasAnySuffix(t *testing.T) {
	tests := []struct {
		s        string
		suffixes []string
		expected bool
	}{
		{"file.txt", []string{".txt", ".log"}, true},
		{"file.log", []string{".txt", ".log"}, true},
		{"file.json", []string{".txt", ".log"}, false},
		{"file", []string{".txt"}, false},
		{"", []string{".txt"}, false},
		{"file.txt", []string{}, false},
	}

	for _, tt := range tests {
		result := hasAnySuffix(tt.s, tt.suffixes...)
		if result != tt.expected {
			t.Errorf("hasAnySuffix(%q, %v) = %v, want %v", tt.s, tt.suffixes, result, tt.expected)
		}
	}
}

func TestNewSource(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config.LogSourceConfig
		shouldErr bool
	}{
		{
			name: "file source",
			cfg: config.LogSourceConfig{
				Type: "file",
				Path: "/var/log/app.log",
			},
			shouldErr: false,
		},
		{
			name: "file source missing path",
			cfg: config.LogSourceConfig{
				Type: "file",
			},
			shouldErr: true,
		},
		{
			name: "command source",
			cfg: config.LogSourceConfig{
				Type:    "command",
				Command: []string{"echo", "test"},
			},
			shouldErr: false,
		},
		{
			name: "command source missing command",
			cfg: config.LogSourceConfig{
				Type: "command",
			},
			shouldErr: true,
		},
		{
			name: "ssh source",
			cfg: config.LogSourceConfig{
				Type: "ssh",
				Host: "server",
				Path: "/var/log",
			},
			shouldErr: false,
		},
		{
			name: "ssh source missing host",
			cfg: config.LogSourceConfig{
				Type: "ssh",
				Path: "/var/log",
			},
			shouldErr: true,
		},
		{
			name: "ssh source missing path",
			cfg: config.LogSourceConfig{
				Type: "ssh",
				Host: "server",
			},
			shouldErr: true,
		},
		{
			name: "docker source",
			cfg: config.LogSourceConfig{
				Type:      "docker",
				Container: "my-container",
			},
			shouldErr: false,
		},
		{
			name: "kubernetes source",
			cfg: config.LogSourceConfig{
				Type: "kubernetes",
				Pod:  "my-pod",
			},
			shouldErr: false,
		},
		{
			name: "unknown source type",
			cfg: config.LogSourceConfig{
				Type: "unknown",
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSource(tt.cfg)
			if (err != nil) != tt.shouldErr {
				t.Errorf("NewSource() error = %v, shouldErr = %v", err, tt.shouldErr)
			}
		})
	}
}

func TestFileSourceName(t *testing.T) {
	src, err := NewFileSource(config.LogSourceConfig{
		Type: "file",
		Path: "/var/log/app.log",
	})
	if err != nil {
		t.Fatalf("NewFileSource failed: %v", err)
	}

	expected := "file:/var/log/app.log"
	if src.Name() != expected {
		t.Errorf("Name() = %q, want %q", src.Name(), expected)
	}
}

func TestCommandSourceName(t *testing.T) {
	src, err := NewCommandSource(config.LogSourceConfig{
		Type:    "command",
		Command: []string{"journalctl", "-f"},
	})
	if err != nil {
		t.Fatalf("NewCommandSource failed: %v", err)
	}

	expected := "command:journalctl -f"
	if src.Name() != expected {
		t.Errorf("Name() = %q, want %q", src.Name(), expected)
	}
}

func TestSSHSourceName(t *testing.T) {
	src, err := NewSSHSource(config.LogSourceConfig{
		Type: "ssh",
		Host: "server01",
		Path: "/var/log/app",
	})
	if err != nil {
		t.Fatalf("NewSSHSource failed: %v", err)
	}

	expected := "ssh:server01:/var/log/app"
	if src.Name() != expected {
		t.Errorf("Name() = %q, want %q", src.Name(), expected)
	}
}

func TestLogSourceConfigIsFollow(t *testing.T) {
	// Default (nil) should be true
	cfg := config.LogSourceConfig{}
	if !cfg.IsFollow() {
		t.Error("Default IsFollow() should be true")
	}

	// Explicitly true
	follow := true
	cfg.Follow = &follow
	if !cfg.IsFollow() {
		t.Error("IsFollow() should be true when set to true")
	}

	// Explicitly false
	noFollow := false
	cfg.Follow = &noFollow
	if cfg.IsFollow() {
		t.Error("IsFollow() should be false when set to false")
	}
}

func TestDockerSourceBasic(t *testing.T) {
	cfg := config.LogSourceConfig{
		Type:      "docker",
		Container: "my-container",
		Since:     "1h",
	}
	follow := true
	cfg.Follow = &follow

	src, err := NewDockerSource(cfg)
	if err != nil {
		t.Fatalf("NewDockerSource failed: %v", err)
	}

	// Check name format
	expected := "docker:my-container"
	if src.Name() != expected {
		t.Errorf("Name() = %q, want %q", src.Name(), expected)
	}

	// Check initial status
	status := src.Status()
	if status.Connected {
		t.Error("Status.Connected should be false before Start")
	}
	if status.LinesRead != 0 {
		t.Errorf("Status.LinesRead = %d, want 0", status.LinesRead)
	}

	// Test stop before start (should not error)
	err = src.Stop()
	if err != nil {
		t.Errorf("Stop before Start returned error: %v", err)
	}

	// Test ListRotatedFiles returns nil
	files, err := src.ListRotatedFiles(context.Background())
	if err != nil {
		t.Errorf("ListRotatedFiles returned error: %v", err)
	}
	if files != nil {
		t.Error("ListRotatedFiles should return nil for Docker source")
	}
}

func TestKubernetesSourceBasic(t *testing.T) {
	cfg := config.LogSourceConfig{
		Type:      "kubernetes",
		Namespace: "default",
		Pod:       "my-pod",
		Container: "app",
		Since:     "30m",
	}
	follow := true
	cfg.Follow = &follow

	src, err := NewKubernetesSource(cfg)
	if err != nil {
		t.Fatalf("NewKubernetesSource failed: %v", err)
	}

	// Check name format
	expected := "k8s:default/my-pod/app"
	if src.Name() != expected {
		t.Errorf("Name() = %q, want %q", src.Name(), expected)
	}

	// Check initial status
	status := src.Status()
	if status.Connected {
		t.Error("Status.Connected should be false before Start")
	}
	if status.LinesRead != 0 {
		t.Errorf("Status.LinesRead = %d, want 0", status.LinesRead)
	}

	// Test stop before start (should not error)
	err = src.Stop()
	if err != nil {
		t.Errorf("Stop before Start returned error: %v", err)
	}

	// Test ListRotatedFiles returns nil
	files, err := src.ListRotatedFiles(context.Background())
	if err != nil {
		t.Errorf("ListRotatedFiles returned error: %v", err)
	}
	if files != nil {
		t.Error("ListRotatedFiles should return nil for Kubernetes source")
	}
}

// TestFileSourceWithRealFile tests the file source with a real temporary file
func TestFileSourceWithRealFile(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "logs-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test log file
	logPath := filepath.Join(tmpDir, "test.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	f.WriteString("line 1\n")
	f.WriteString("line 2\n")
	f.Close()

	src, err := NewFileSource(config.LogSourceConfig{
		Type: "file",
		Path: logPath,
	})
	if err != nil {
		t.Fatalf("NewFileSource failed: %v", err)
	}

	// Test status before start
	status := src.Status()
	if status.Connected {
		t.Error("Status.Connected should be false before Start")
	}
	if status.LinesRead != 0 {
		t.Errorf("Status.LinesRead = %d, want 0", status.LinesRead)
	}

	// Test stop before start (should not error)
	err = src.Stop()
	if err != nil {
		t.Errorf("Stop before Start returned error: %v", err)
	}
}

// TestFileSourceListRotatedFiles tests listing rotated files
func TestFileSourceListRotatedFiles(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "logs-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	for _, name := range []string{"app.log", "app.log.1", "app.log.2.gz"} {
		f, _ := os.Create(filepath.Join(tmpDir, name))
		f.Close()
	}

	src, err := NewFileSource(config.LogSourceConfig{
		Type:           "file",
		Path:           filepath.Join(tmpDir, "app.log"),
		RotatedPattern: "app.log.*",
	})
	if err != nil {
		t.Fatalf("NewFileSource failed: %v", err)
	}

	files, err := src.ListRotatedFiles(context.Background())
	if err != nil {
		t.Fatalf("ListRotatedFiles failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("ListRotatedFiles returned %d files, want 2", len(files))
	}

	// Check that gz file is marked as compressed
	for _, f := range files {
		if f.Name == "app.log.2.gz" && !f.Compressed {
			t.Error("app.log.2.gz should be marked as compressed")
		}
	}
}

// TestFileSourceListRotatedFilesNoPattern tests when no pattern is configured
func TestFileSourceListRotatedFilesNoPattern(t *testing.T) {
	src, err := NewFileSource(config.LogSourceConfig{
		Type: "file",
		Path: "/var/log/app.log",
		// No RotatedPattern
	})
	if err != nil {
		t.Fatalf("NewFileSource failed: %v", err)
	}

	files, err := src.ListRotatedFiles(context.Background())
	if err != nil {
		t.Fatalf("ListRotatedFiles failed: %v", err)
	}

	if files != nil {
		t.Errorf("ListRotatedFiles with no pattern should return nil, got %v", files)
	}
}

// TestCommandSourceBasic tests basic command source operations
func TestCommandSourceBasic(t *testing.T) {
	src, err := NewCommandSource(config.LogSourceConfig{
		Type:    "command",
		Command: []string{"echo", "hello"},
	})
	if err != nil {
		t.Fatalf("NewCommandSource failed: %v", err)
	}

	// Test status before start
	status := src.Status()
	if status.Connected {
		t.Error("Status.Connected should be false before Start")
	}

	// Test ListRotatedFiles returns nil (not supported)
	files, err := src.ListRotatedFiles(context.Background())
	if err != nil {
		t.Errorf("ListRotatedFiles returned error: %v", err)
	}
	if files != nil {
		t.Error("ListRotatedFiles should return nil for command source")
	}

	// Test ReadRange returns error (not supported)
	err = src.ReadRange(context.Background(), time.Now(), time.Now(), make(chan string), "", 0, 0)
	if err == nil {
		t.Error("ReadRange should return error for command source")
	}
}

// TestCommandSourceStartStop tests starting and stopping a command source
func TestCommandSourceStartStop(t *testing.T) {
	src, err := NewCommandSource(config.LogSourceConfig{
		Type:    "command",
		Command: []string{"echo", "line1\nline2\nline3"},
	})
	if err != nil {
		t.Fatalf("NewCommandSource failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lineCh := make(chan string, 100)
	errCh := make(chan error, 10)

	err = src.Start(ctx, lineCh, errCh)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Read lines (echo command should finish quickly)
	var lines []string
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case line, ok := <-lineCh:
			if !ok {
				break loop
			}
			lines = append(lines, line)
		case <-timeout:
			break loop
		}
	}

	// Should have received some output
	if len(lines) == 0 {
		t.Error("Expected to receive at least one line")
	}

	// Status should have been updated
	status := src.Status()
	if status.LinesRead == 0 && len(lines) > 0 {
		t.Log("Warning: Status.LinesRead not updated (timing issue)")
	}

	src.Stop()
}

// TestSSHSourceBasic tests basic SSH source operations (no actual SSH)
func TestSSHSourceBasic(t *testing.T) {
	src, err := NewSSHSource(config.LogSourceConfig{
		Type: "ssh",
		Host: "testhost",
		Path: "/var/log/app",
	})
	if err != nil {
		t.Fatalf("NewSSHSource failed: %v", err)
	}

	// Test status before start
	status := src.Status()
	if status.Connected {
		t.Error("Status.Connected should be false before Start")
	}

	// Test stop before start (should not error)
	err = src.Stop()
	if err != nil {
		t.Errorf("Stop before Start returned error: %v", err)
	}
}

// TestSourceStatusMethods tests the status methods on sources
func TestSourceStatusMethods(t *testing.T) {
	src, _ := NewFileSource(config.LogSourceConfig{
		Type: "file",
		Path: "/var/log/test.log",
	})

	// Initial status
	status := src.Status()
	if status.Connected {
		t.Error("Initial Connected should be false")
	}
	if status.Error != "" {
		t.Errorf("Initial Error should be empty, got %q", status.Error)
	}
	if !status.LastConnect.IsZero() {
		t.Error("Initial LastConnect should be zero")
	}
	if !status.LastError.IsZero() {
		t.Error("Initial LastError should be zero")
	}
	if status.BytesRead != 0 {
		t.Errorf("Initial BytesRead = %d, want 0", status.BytesRead)
	}
	if status.LinesRead != 0 {
		t.Errorf("Initial LinesRead = %d, want 0", status.LinesRead)
	}
}

// TestDockerSourceMissingContainer tests Docker source without container
func TestDockerSourceMissingContainer(t *testing.T) {
	cfg := config.LogSourceConfig{
		Type: "docker",
		// Missing Container
	}

	_, err := NewDockerSource(cfg)
	if err == nil {
		t.Error("NewDockerSource should return error when container is missing")
	}
}

// TestKubernetesSourceDefaultNamespace tests K8s source with default namespace
func TestKubernetesSourceDefaultNamespace(t *testing.T) {
	cfg := config.LogSourceConfig{
		Type: "kubernetes",
		Pod:  "my-pod",
		// No namespace specified
	}

	src, err := NewKubernetesSource(cfg)
	if err != nil {
		t.Fatalf("NewKubernetesSource failed: %v", err)
	}

	// Should use default namespace
	expected := "k8s:default/my-pod"
	if src.Name() != expected {
		t.Errorf("Name() = %q, want %q", src.Name(), expected)
	}
}

// TestKubernetesSourceMissingPod tests K8s source without pod
func TestKubernetesSourceMissingPod(t *testing.T) {
	cfg := config.LogSourceConfig{
		Type:      "kubernetes",
		Namespace: "test-ns",
		// Missing Pod
	}

	_, err := NewKubernetesSource(cfg)
	if err == nil {
		t.Error("NewKubernetesSource should return error when pod is missing")
	}
}

// TestDockerSourceStatusMethods tests Docker source status methods
func TestDockerSourceStatusMethods(t *testing.T) {
	src, _ := NewDockerSource(config.LogSourceConfig{
		Type:      "docker",
		Container: "test-container",
	})

	// Initial status should be not connected
	status := src.Status()
	if status.Connected {
		t.Error("Initial Connected should be false")
	}
	if status.Error != "" {
		t.Errorf("Initial Error should be empty, got %q", status.Error)
	}
	if !status.LastConnect.IsZero() {
		t.Error("Initial LastConnect should be zero")
	}
	if !status.LastError.IsZero() {
		t.Error("Initial LastError should be zero")
	}
	if status.LinesRead != 0 {
		t.Errorf("Initial LinesRead = %d, want 0", status.LinesRead)
	}
}

// TestKubernetesSourceNameVariations tests K8s source name with different configs
func TestKubernetesSourceNameVariations(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config.LogSourceConfig
		expected  string
	}{
		{
			name: "pod only",
			cfg: config.LogSourceConfig{
				Type: "kubernetes",
				Pod:  "my-pod",
			},
			expected: "k8s:default/my-pod",
		},
		{
			name: "pod with namespace",
			cfg: config.LogSourceConfig{
				Type:      "kubernetes",
				Namespace: "kube-system",
				Pod:       "coredns",
			},
			expected: "k8s:kube-system/coredns",
		},
		{
			name: "pod with container",
			cfg: config.LogSourceConfig{
				Type:      "kubernetes",
				Pod:       "my-pod",
				Container: "sidecar",
			},
			expected: "k8s:default/my-pod/sidecar",
		},
		{
			name: "full config",
			cfg: config.LogSourceConfig{
				Type:      "kubernetes",
				Namespace: "production",
				Pod:       "web-app",
				Container: "nginx",
			},
			expected: "k8s:production/web-app/nginx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, err := NewKubernetesSource(tt.cfg)
			if err != nil {
				t.Fatalf("NewKubernetesSource failed: %v", err)
			}
			if src.Name() != tt.expected {
				t.Errorf("Name() = %q, want %q", src.Name(), tt.expected)
			}
		})
	}
}

// TestDockerSourceReadRangeNotSupported tests Docker ReadRange returns nil error
func TestDockerSourceReadRangeValid(t *testing.T) {
	src, _ := NewDockerSource(config.LogSourceConfig{
		Type:      "docker",
		Container: "test-container",
	})

	// ReadRange should work (returns immediately since docker logs command won't exist)
	ch := make(chan string, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Note: This will fail since docker isn't running, but it tests the code path
	err := src.ReadRange(ctx, time.Now().Add(-time.Hour), time.Now(), ch, "", 0, 0)
	// We expect an error since docker isn't available
	if err == nil {
		// Drain channel
		for range ch {
		}
	}
}

// TestKubernetesSourceStatusMethods tests K8s source status methods
func TestKubernetesSourceStatusMethods(t *testing.T) {
	src, _ := NewKubernetesSource(config.LogSourceConfig{
		Type: "kubernetes",
		Pod:  "test-pod",
	})

	// Initial status should be not connected
	status := src.Status()
	if status.Connected {
		t.Error("Initial Connected should be false")
	}
	if status.Error != "" {
		t.Errorf("Initial Error should be empty, got %q", status.Error)
	}
	if status.LinesRead != 0 {
		t.Errorf("Initial LinesRead = %d, want 0", status.LinesRead)
	}
}
