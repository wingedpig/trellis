// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package middleware

import "net/http"

// BodyLimit returns middleware that caps request body size via
// http.MaxBytesReader, so unbounded json.NewDecoder/ParseMultipartForm
// reads can't exhaust memory or disk. Reads past the limit fail and the
// connection is closed. GET/WebSocket requests carry no body and are
// unaffected.
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}
