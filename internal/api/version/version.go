// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package version implements Stripe-style API versioning for the Trellis API.
//
// Versioning uses date-based versions (e.g., "2026-01-17") sent via the
// Trellis-Version header. When no header is provided, the latest version
// is used.
//
// When making breaking changes:
//  1. Create a new version constant with today's date
//  2. Update LatestVersion to the new version
//  3. Add a transformer in transformer.go for the old version
//
// Example:
//
//	const Version20260301 = "2026-03-01"  // New version
//	var LatestVersion = Version20260301
//
// Then add a transformer to convert new responses back to old format
// for clients pinned to older versions.
package version

import "context"

// Version constants. Add new versions here when making breaking changes.
const (
	// Version20260117 is the initial API version.
	Version20260117 = "2026-01-17"
)

// LatestVersion is the current default API version.
// Update this when adding a new version.
var LatestVersion = Version20260117

// Header is the HTTP header used to specify the API version.
const Header = "Trellis-Version"

// contextKey is the type used for context keys in this package.
type contextKey string

// versionKey is the context key for storing the API version.
const versionKey contextKey = "api-version"

// FromContext returns the API version from the context.
// Returns LatestVersion if not set.
func FromContext(ctx context.Context) string {
	v, ok := ctx.Value(versionKey).(string)
	if !ok || v == "" {
		return LatestVersion
	}
	return v
}

// WithContext returns a new context with the API version set.
func WithContext(ctx context.Context, version string) context.Context {
	return context.WithValue(ctx, versionKey, version)
}
