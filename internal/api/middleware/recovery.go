// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
)

// Recovery is middleware that recovers from panics.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic recovered: %v\n%s", err, debug.Stack())

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR","message":"Internal server error"}}`))
			}
		}()

		next.ServeHTTP(w, r)
	})
}
