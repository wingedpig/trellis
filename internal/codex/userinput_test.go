// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"encoding/json"
	"testing"
)

// Text inputs must always carry text_elements; localImage inputs carry only
// type and path.
func TestUserInputWireShape(t *testing.T) {
	in := []UserInput{
		{Type: "text", Text: "hi", TextElements: &[]interface{}{}},
		{Type: "localImage", Path: "/tmp/a.png"},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"text","text":"hi","text_elements":[]},{"type":"localImage","path":"/tmp/a.png"}]`
	if string(data) != want {
		t.Errorf("got  %s\nwant %s", data, want)
	}
}
