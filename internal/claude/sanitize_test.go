// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"testing"
)

// TestSanitizeMessage_dropsInvalidRawMessage verifies the fix for the
// "claude: failed to marshal server message (type=history): json: error
// calling MarshalJSON for type json.RawMessage: invalid character …"
// regression. A tool_use block whose Input is non-JSON bytes used to make
// every subsequent json.Marshal of the history payload fail.
func TestSanitizeMessage_dropsInvalidRawMessage(t *testing.T) {
	msg := Message{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "tool_use", ID: "id1", Name: "Bash", Input: json.RawMessage(`1: "not valid"`)},
			{Type: "text", Text: "world"},
		},
	}

	// Before sanitize: json.Marshal must fail because RawMessage validates
	// its bytes during MarshalJSON.
	if _, err := json.Marshal(msg); err == nil {
		t.Fatal("expected pre-sanitize marshal to fail with invalid RawMessage")
	}

	clean := sanitizeMessage(msg)
	if _, err := json.Marshal(clean); err != nil {
		t.Fatalf("post-sanitize marshal failed: %v", err)
	}

	// The good neighbors are preserved.
	if clean.Content[0].Text != "hello" || clean.Content[2].Text != "world" {
		t.Errorf("sanitize disturbed sibling blocks: %+v", clean.Content)
	}
	// The bad block keeps its identity but loses the bad Input.
	if clean.Content[1].ID != "id1" || clean.Content[1].Name != "Bash" {
		t.Errorf("sanitize lost block identity: %+v", clean.Content[1])
	}
	if clean.Content[1].Input != nil {
		t.Errorf("sanitize kept invalid Input: %s", string(clean.Content[1].Input))
	}

	// Sanitize MUST NOT mutate the original — the in-memory record stays
	// intact even if we can't ship it over the wire as-is.
	if msg.Content[1].Input == nil {
		t.Error("sanitize mutated the original message's Input")
	}
}

func TestSanitizeMessage_preservesValidInput(t *testing.T) {
	good := json.RawMessage(`{"command":"ls"}`)
	msg := Message{
		Role: "assistant",
		Content: []ContentBlock{
			{Type: "tool_use", ID: "id", Name: "Bash", Input: good},
		},
	}
	clean := sanitizeMessage(msg)
	if string(clean.Content[0].Input) != string(good) {
		t.Errorf("valid Input was disturbed: %s", string(clean.Content[0].Input))
	}
}

func TestSafeRawInput(t *testing.T) {
	cases := []struct {
		name string
		in   json.RawMessage
		want json.RawMessage
	}{
		{"empty stays nil", json.RawMessage(``), nil},
		{"nil stays nil", nil, nil},
		{"valid object passes through", json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":1}`)},
		{"valid number passes through", json.RawMessage(`42`), json.RawMessage(`42`)},
		{"invalid bytes → nil", json.RawMessage(`1: "no"`), nil},
		{"half-built object → nil", json.RawMessage(`{"k":`), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := safeRawInput(tc.in)
			if string(got) != string(tc.want) {
				t.Errorf("safeRawInput(%q) = %q, want %q", string(tc.in), string(got), string(tc.want))
			}
		})
	}
}
