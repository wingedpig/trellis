// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package validate provides shared validators for user-supplied names that
// end up as arguments to external commands (git, tmux). The argv exec form
// used throughout Trellis blocks shell injection, but a name with a leading
// '-' would still be parsed as a flag by the target command (argument
// injection). These validators reject such names before any args are built.
package validate

import (
	"fmt"
	"regexp"
)

// namePattern requires a leading alphanumeric (rejecting the leading '-'
// that git/tmux would parse as a flag), followed by alphanumerics plus
// '.', '_', '/', '-'.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// Name validates a user-supplied name (branch, window, session) destined for
// an external command's argv. kind labels the name in the error message.
func Name(kind, s string) error {
	if s == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if !namePattern.MatchString(s) {
		return fmt.Errorf("invalid %s name %q: must start with a letter or digit and contain only letters, digits, '.', '_', '/', '-'", kind, s)
	}
	return nil
}
