// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemotePipelineCommand(t *testing.T) {
	tests := []struct {
		name       string
		p          remotePipeline
		decompress string
		want       string
	}{
		{"nothing, plain", remotePipeline{}, "", "cat '/l/a.log'"},
		{"nothing, compressed", remotePipeline{}, "zstd -dc", "zstd -dc '/l/a.log'"},
		{
			"all stages, compressed",
			remotePipeline{timestamp: "2026-11-08[T ]06:", literals: []string{"STRT"}, userGrep: "STRT.*x"},
			"zstd -dc",
			"zstd -dc '/l/a.log' | env LC_ALL=C grep -F -e 'STRT' | env LC_ALL=C grep -E -- '2026-11-08[T ]06:' | grep -E -- 'STRT.*x' || true",
		},
		{
			"literals first reads the file",
			remotePipeline{literals: []string{"a'b", "END"}, foldLiterals: true, userGrep: "x"},
			"",
			`env LC_ALL=C grep -F -i -e 'a'\''b' -e 'END' '/l/a.log' | grep -E -- 'x' || true`,
		},
		{
			"context lines",
			remotePipeline{timestamp: "ts", userGrep: "err", before: 2, after: 3},
			"",
			"env LC_ALL=C grep -E -- 'ts' '/l/a.log' | grep -E -B 2 -A 3 -- 'err' || true",
		},
	}
	for _, tt := range tests {
		if got := tt.p.command("/l/a.log", tt.decompress); got != tt.want {
			t.Errorf("%s:\n got  %s\n want %s", tt.name, got, tt.want)
		}
	}
}

// TestRemotePipelineRuns runs generated commands under a local shell against
// a real zstd file, as the log host would.
func TestRemotePipelineRuns(t *testing.T) {
	if _, err := exec.LookPath("zstd"); err != nil {
		t.Skip("zstd not installed")
	}
	dir := t.TempDir()
	plain := filepath.Join(dir, "app.log")
	lines := []string{
		"2026-11-08T05:59:59Z STRT id=0 path=/api/a",
		"2026-11-08T06:00:01Z STRT id=1 path=/api/a",
		"2026-11-08T06:00:02Z sql SELECT id=1",
		"2026-11-08T06:00:03Z END id=1 status=200",
		"2026-11-08T06:00:04Z strt id=2 lowercase",
		"2026-11-08T07:00:00Z STRT id=3 path=/api/b",
	}
	if err := os.WriteFile(plain, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("zstd", "-q", "-k", plain).CombinedOutput(); err != nil {
		t.Fatalf("zstd: %v: %s", err, out)
	}

	run := func(p remotePipeline, path, decompress string) []string {
		t.Helper()
		out, err := exec.Command("sh", "-c", p.command(path, decompress)).Output()
		if err != nil {
			t.Fatalf("%s: %v", p.command(path, decompress), err)
		}
		return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(out)), " ", "_"))
	}
	ids := func(got []string) string {
		var out []string
		for _, l := range got {
			for _, f := range strings.Split(l, "_") {
				if strings.HasPrefix(f, "id=") {
					out = append(out, strings.TrimPrefix(f, "id="))
				}
			}
		}
		return strings.Join(out, ",")
	}

	pattern := `STRT.*path=/api/`
	lits, fold, _ := RequiredLiterals(pattern)
	ere, _ := translateToERE(pattern)
	p := remotePipeline{timestamp: "2026-11-08[T ]06:", literals: lits, foldLiterals: fold, userGrep: ere}
	if got := ids(run(p, plain+".zst", "zstd -dc")); got != "1" {
		t.Errorf("compressed: ids %q, want 1", got)
	}
	if got := ids(run(p, plain, "")); got != "1" {
		t.Errorf("plain: ids %q, want 1", got)
	}

	// Case-insensitive pattern: the folded literal must keep "strt".
	pattern = `(?i)strt`
	lits, fold, _ = RequiredLiterals(pattern)
	ere, _ = translateToERE(pattern)
	p = remotePipeline{literals: lits, foldLiterals: fold, userGrep: ere}
	if got := ids(run(p, plain+".zst", "zstd -dc")); got != "0,1,2,3" {
		t.Errorf("fold: ids %q, want 0,1,2,3", got)
	}

	// A requests-style alternation prefilter.
	pattern = `(?:STRT)|(?:END)|(?:SELECT)`
	lits, fold, _ = RequiredLiterals(pattern)
	ere, _ = translateToERE(pattern)
	p = remotePipeline{timestamp: "2026-11-08[T ]06:", literals: lits, foldLiterals: fold, userGrep: ere}
	if got := ids(run(p, plain+".zst", "zstd -dc")); got != "1,1,1" {
		t.Errorf("alternation: ids %q, want 1,1,1", got)
	}
}

func TestReadFilesOrdered(t *testing.T) {
	var files []RotatedFile
	for i := range 10 {
		files = append(files, RotatedFile{Name: fmt.Sprintf("f%d", i)})
	}
	var running, peak atomic.Int32
	read := func(ctx context.Context, f RotatedFile, out chan<- string) error {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		// Later files finish first, so order must come from the reader.
		var idx int
		fmt.Sscanf(f.Name, "f%d", &idx)
		time.Sleep(time.Duration(10-idx) * 3 * time.Millisecond)
		for j := range 3 {
			select {
			case out <- fmt.Sprintf("%s-%d", f.Name, j):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	lineCh := make(chan string, 100)
	if err := readFilesOrdered(context.Background(), files, read, lineCh); err != nil {
		t.Fatal(err)
	}
	close(lineCh)
	var got []string
	for l := range lineCh {
		got = append(got, l)
	}
	var want []string
	for _, f := range files {
		for j := range 3 {
			want = append(want, fmt.Sprintf("%s-%d", f.Name, j))
		}
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("order:\n got  %v\n want %v", got, want)
	}
	if p := peak.Load(); p < 2 || p > rangeReadParallelism {
		t.Errorf("peak concurrency %d, want 2..%d", p, rangeReadParallelism)
	}
}

func TestReadFilesOrderedStopsAtFailure(t *testing.T) {
	files := []RotatedFile{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	boom := errors.New("boom")
	read := func(ctx context.Context, f RotatedFile, out chan<- string) error {
		if f.Name == "b" {
			out <- "b-partial"
			return boom
		}
		out <- f.Name
		return nil
	}
	lineCh := make(chan string, 10)
	err := readFilesOrdered(context.Background(), files, read, lineCh)
	close(lineCh)
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "reading b") {
		t.Fatalf("err = %v, want reading b: boom", err)
	}
	var got []string
	for l := range lineCh {
		got = append(got, l)
	}
	if strings.Join(got, ",") != "a,b-partial" {
		t.Errorf("lines = %v, want a and b's partial output only", got)
	}
}

func TestReadFilesOrderedHonorsDeadline(t *testing.T) {
	files := []RotatedFile{{Name: "fast"}, {Name: "slow"}}
	read := func(ctx context.Context, f RotatedFile, out chan<- string) error {
		if f.Name == "slow" {
			<-ctx.Done()
			return ctx.Err()
		}
		out <- "fast"
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	lineCh := make(chan string, 10)
	start := time.Now()
	err := readFilesOrdered(ctx, files, read, lineCh)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v; the deadline should stop a file in flight", time.Since(start))
	}
	if len(lineCh) != 1 {
		t.Errorf("got %d lines, want the fast file's 1", len(lineCh))
	}
}
