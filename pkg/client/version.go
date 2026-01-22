// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

// API version constants.
//
// Trellis uses Stripe-style date-based API versioning. Each version represents
// the API as it existed on that date. Clients can pin to a specific version
// to ensure backwards compatibility as the API evolves.
//
// When making a request, the client sends the version via the Trellis-Version
// header. If no version is specified, the latest version is used.
const (
	// LatestVersion is the current API version.
	// New clients should use this unless they need to pin to an older version.
	LatestVersion = "2026-01-17"

	// Version20260117 is the initial API version.
	Version20260117 = "2026-01-17"
)

// VersionHeader is the HTTP header used to specify the API version.
const VersionHeader = "Trellis-Version"
