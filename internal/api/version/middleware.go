// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package version

import "net/http"

// Middleware extracts the API version from the request header and
// stores it in the request context. If no version header is present,
// the latest version is used.
//
// Usage:
//
//	router.Use(version.Middleware)
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := r.Header.Get(Header)
		if version == "" {
			version = LatestVersion
		}

		// Store version in context
		ctx := WithContext(r.Context(), version)

		// Set response header to confirm version being used
		w.Header().Set(Header, version)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
