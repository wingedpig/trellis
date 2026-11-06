// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wingedpig/trellis/internal/config"
)

// prefilterSource claims to grep server-side but returns every line, like a
// remote grep whose prefilter is broader than the Go pattern.
type prefilterSource struct {
	lines    []string
	gotGrep  string
	validate error
}

func (s *prefilterSource) Name() string { return "prefilter" }
func (s *prefilterSource) Start(ctx context.Context, lineCh chan<- string, errCh chan<- error) error {
	return nil
}
func (s *prefilterSource) Stop() error          { return nil }
func (s *prefilterSource) Status() SourceStatus { return SourceStatus{} }
func (s *prefilterSource) ListRotatedFiles(ctx context.Context) ([]RotatedFile, error) {
	return nil, nil
}
func (s *prefilterSource) HandlesGrep() bool { return true }
func (s *prefilterSource) ValidateGrep(string, int, int) error {
	return s.validate
}
func (s *prefilterSource) ReadRange(ctx context.Context, start, end time.Time, lineCh chan<- string, grep string, before, after int) error {
	s.gotGrep = grep
	for _, l := range s.lines {
		lineCh <- l
	}
	return nil
}

func newPrefilterViewer(t *testing.T, src *prefilterSource) *Viewer {
	t.Helper()
	v, err := NewViewerWithSource(config.LogViewerConfig{
		Name:   "karld01",
		Parser: config.LogParserConfig{Type: "json", Timestamp: "time", Message: "msg"},
	}, src)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHistoryGrepRechecksPrefilteredLines(t *testing.T) {
	now := time.Now()
	line := func(to string) string {
		return fmt.Sprintf(`{"time":%q,"msg":"delivered","to":%q}`, now.Format(time.RFC3339Nano), to)
	}
	src := &prefilterSource{lines: []string{line("TAL01@ca.org"), line("tal01@caXorg"), line("other@ca.org")}}
	v := newPrefilterViewer(t, src)

	got, err := v.GetHistoricalEntries(context.Background(), now.Add(-time.Minute), now.Add(time.Minute),
		nil, 0, `(?i)tal01@ca\.org`, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Fields["to"] != "TAL01@ca.org" {
		t.Fatalf("got %d entries %+v, want only TAL01@ca.org", len(got), got)
	}

	// With context lines the source's output is trusted as-is.
	got, err = v.GetHistoricalEntries(context.Background(), now.Add(-time.Minute), now.Add(time.Minute),
		nil, 0, `(?i)tal01@ca\.org`, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("with context: got %d entries, want 3", len(got))
	}
}

func TestHistoryGrepRejectsInvalidPattern(t *testing.T) {
	src := &prefilterSource{}
	v := newPrefilterViewer(t, src)
	now := time.Now()
	_, err := v.GetHistoricalEntries(context.Background(), now.Add(-time.Minute), now, nil, 0, `(?i`, 0, 0)
	if !IsGrepPatternError(err) {
		t.Fatalf("err = %v, want grep pattern error", err)
	}
	if src.gotGrep != "" {
		t.Fatal("source was read despite an invalid pattern")
	}
}

func TestSSHRemoteGrepPattern(t *testing.T) {
	s := &SSHSource{cfg: config.LogSourceConfig{Host: "karld01"}}
	got, err := s.remoteGrepPattern(`(?i)tal01@ca\.org`, 0, 0)
	if err != nil || got != `[Tt][Aa][Ll]01@[Cc][Aa]\.[Oo][Rr][Gg]` {
		t.Fatalf("remoteGrepPattern = %q, %v", got, err)
	}
	// Untranslatable without context: no remote grep, Go filter does the work.
	if got, err := s.remoteGrepPattern(`\p{Greek}`, 0, 0); err != nil || got != "" {
		t.Fatalf("untranslatable = %q, %v; want empty, nil", got, err)
	}
	// Untranslatable with context lines can't be honored.
	if _, err := s.remoteGrepPattern(`\p{Greek}`, 2, 0); !IsGrepPatternError(err) {
		t.Fatalf("untranslatable with context: err = %v, want grep pattern error", err)
	}
	if _, err := s.remoteGrepPattern(`(unclosed`, 0, 0); !IsGrepPatternError(err) {
		t.Fatalf("invalid: err = %v, want grep pattern error", err)
	}
}
