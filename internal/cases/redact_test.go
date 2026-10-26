// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Fake secrets are assembled at runtime so this file doesn't trip secret
// scanners itself.
var (
	fakeAWS    = "AKIA" + "ABCDEFGHIJKLMNOP"
	fakeStripe = "sk_" + "live_" + "abcdefghijklmnop1234"
	fakeWhsec  = "whsec_" + "f5a1b2c3d4e5f6a7b8c9d0"
	fakeGitHub = "ghp_" + strings.Repeat("a1B2", 9)
	fakeAnth   = "sk-ant-" + "api03-abcdefghijklmnopqrstuvwxyz"
)

func TestRedactText_rules(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"aws", "key " + fakeAWS + " end", "key [REDACTED:aws-access-key] end"},
		{"stripe", "STRIPE=" + fakeStripe, "STRIPE=[REDACTED:stripe-key]"},
		{"webhook secret", "secret: " + fakeWhsec, "secret: [REDACTED:stripe-webhook-secret]"},
		{"github", "token " + fakeGitHub, "token [REDACTED:github-token]"},
		{"anthropic before openai", fakeAnth, "[REDACTED:anthropic-key]"},
		{"url password keeps user and host", "postgres://app:hunter2pw@db:5432/x", "postgres://app:[REDACTED:url-password]@db:5432/x"},
		{"assigned literal", `password = "correct-horse"`, `password = "[REDACTED:assigned-secret]"`},
		{"assigned go short decl", `apiKey := "abcdef123456"`, `apiKey := "[REDACTED:assigned-secret]"`},
		{"code reference left alone", `password := req.Password`, `password := req.Password`},
		{"short literal left alone", `token = "abc"`, `token = "abc"`},
		{"prose left alone", "the secret to good tests is patience", "the secret to good tests is patience"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := redactText(tt.in, nil)
			if got != tt.want {
				t.Errorf("redactText(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// Transcripts are JSONL; masking must leave every line valid JSON, including
// escaped private keys and quoted key/value pairs inside tool output.
func TestRedactText_keepsJSONValid(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIICXgIBAAKBgQDU8wdiaFmPfTyRYuFlVPi8\nAbC/+=\n-----END RSA PRIVATE KEY-----"
	payload := map[string]any{
		"tool_output": "cat .env\nSTRIPE_KEY=" + fakeStripe + "\nAWS=" + fakeAWS + "\n",
		"input":       map[string]string{"content": pem + "\nafter"},
		"nested":      `{"password": "s3cretValue!", "user": "bob"}`,
	}
	line, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	out, counts := redactText(string(line), nil)
	if !json.Valid([]byte(out)) {
		t.Fatalf("redacted line is not valid JSON:\n%s", out)
	}
	for _, leaked := range []string{fakeStripe, fakeAWS, "MIICXgIBAAKBgQDU8wdiaFmPfTyRYuFlVPi8", "s3cretValue!"} {
		if strings.Contains(out, leaked) {
			t.Errorf("secret %q survived redaction:\n%s", leaked, out)
		}
	}
	if !strings.Contains(out, `after`) || !strings.Contains(out, `bob`) {
		t.Errorf("redaction ate surrounding text:\n%s", out)
	}
	for _, k := range []string{"stripe-key", "aws-access-key", "private-key", "assigned-secret"} {
		if counts[k] == 0 {
			t.Errorf("counts[%q] = 0, want > 0 (counts: %v)", k, counts)
		}
	}
}

func TestRedactText_idempotent(t *testing.T) {
	extra := []*regexp.Regexp{regexp.MustCompile(`internal-token=(\S+)`)}
	once, c1 := redactText("x "+fakeAWS+" internal-token=zzz", extra)
	twice, c2 := redactText(once, extra)
	if once != twice {
		t.Errorf("second pass changed text:\n%q\n%q", once, twice)
	}
	if c1["custom"] != 1 || c2 != nil {
		t.Errorf("counts: first %v, second %v; want custom=1 then nil", c1, c2)
	}
}

func TestArchive_redactsAndRecordsCounts(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	c, err := m.Create(wt, "Leaky", "bug", "wt1", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	caseDir := filepath.Join(wt, "cases", c.ID)
	notes := filepath.Join(caseDir, "notes.md")
	if err := os.WriteFile(notes, []byte("found key "+fakeAWS+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	png := filepath.Join(caseDir, "shot.png")
	binary := append([]byte{0x89, 'P', 'N', 'G', 0}, []byte(fakeAWS)...)
	if err := os.WriteFile(png, binary, 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := m.Archive(wt, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(wt, "cases-archived", id)
	got, _ := os.ReadFile(filepath.Join(archived, "notes.md"))
	if strings.Contains(string(got), fakeAWS) {
		t.Errorf("notes.md still holds the key: %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(archived, "shot.png")); string(b) != string(binary) {
		t.Errorf("binary file was rewritten")
	}
	cj, err := m.Get(wt, id)
	if err != nil {
		t.Fatal(err)
	}
	if cj.Redactions["aws-access-key"] != 1 {
		t.Errorf("Redactions = %v, want aws-access-key=1", cj.Redactions)
	}
}

func TestArchive_redactionDisabled(t *testing.T) {
	m, wt := newManagerWithTempDir(t)
	if err := m.SetRedaction(false, nil); err != nil {
		t.Fatal(err)
	}
	c, _ := m.Create(wt, "Off", "bug", "wt1", "main", "")
	notes := filepath.Join(wt, "cases", c.ID, "notes.md")
	os.WriteFile(notes, []byte(fakeAWS), 0o644)
	id, err := m.Archive(wt, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(wt, "cases-archived", id, "notes.md"))
	if string(got) != fakeAWS {
		t.Errorf("notes.md = %q, want it untouched", got)
	}
}
