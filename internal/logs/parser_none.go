// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package logs

import (
	"time"
)

// NoneParser treats each line as a raw message without parsing.
type NoneParser struct{}

// NewNoneParser creates a new none/raw parser.
func NewNoneParser() *NoneParser {
	return &NoneParser{}
}

// Name returns the parser name.
func (p *NoneParser) Name() string {
	return "none"
}

// Parse returns a LogEntry with the raw line as the message.
func (p *NoneParser) Parse(line string) LogEntry {
	return LogEntry{
		Raw:       line,
		Message:   line,
		Timestamp: time.Now(),
	}
}
