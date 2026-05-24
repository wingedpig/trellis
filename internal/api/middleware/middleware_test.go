// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogging(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello"))
	})

	wrapped := Logging(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "hello", rec.Body.String())
}

func TestLogging_StatusCapture(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	wrapped := Logging(handler)

	req := httptest.NewRequest("GET", "/notfound", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRecovery(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	wrapped := Recovery(handler)

	req := httptest.NewRequest("GET", "/panic", nil)
	rec := httptest.NewRecorder()

	// Should not panic
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "INTERNAL_ERROR")
}

func TestRecovery_NoPanic(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	wrapped := Recovery(handler)

	req := httptest.NewRequest("GET", "/ok", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestCORS_SameOrigin(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CORS(CORSConfig{})(handler)

	req := httptest.NewRequest("GET", "http://localhost:1234/test", nil)
	req.Host = "localhost:1234"
	req.Header.Set("Origin", "http://localhost:1234")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, "http://localhost:1234", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "GET")
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCORS_CrossOriginRejected(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CORS(CORSConfig{})(handler)

	req := httptest.NewRequest("POST", "http://localhost:1234/test", nil)
	req.Host = "localhost:1234"
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.False(t, called, "handler must not run for disallowed origin")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_AllowedOrigin(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CORS(CORSConfig{AllowedOrigins: []string{"https://trellis.example.com"}})(handler)

	req := httptest.NewRequest("GET", "http://localhost:1234/test", nil)
	req.Host = "localhost:1234"
	req.Header.Set("Origin", "https://trellis.example.com")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, "https://trellis.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_Preflight(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for OPTIONS")
	})

	wrapped := CORS(CORSConfig{})(handler)

	req := httptest.NewRequest("OPTIONS", "http://localhost:1234/test", nil)
	req.Host = "localhost:1234"
	req.Header.Set("Origin", "http://localhost:1234")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://localhost:1234", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORS_NoOriginPassthrough(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CORS(CORSConfig{})(handler)

	req := httptest.NewRequest("GET", "http://localhost:1234/test", nil)
	req.Host = "localhost:1234"
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, "", rec.Header().Get("Access-Control-Allow-Origin"))
}

// TestCORS_DNSRebindingHostRejected covers the DNS-rebinding scenario: the
// attacker's hostname resolves to 127.0.0.1, so the browser sends both
// Origin and Host as that hostname. Neither matches loopback nor the
// allow-list, so the request is rejected before the handler runs — even
// though "Origin" and "Host" agree, which is what the old r.Host shortcut
// would have trusted.
func TestCORS_DNSRebindingHostRejected(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CORS(CORSConfig{})(handler)

	req := httptest.NewRequest("POST", "http://attacker.example.com/api/v1/workflows/build/run", nil)
	req.Host = "attacker.example.com"
	req.Header.Set("Origin", "http://attacker.example.com")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.False(t, called, "handler must not run when Host is not in allow-list")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestCORS_PermitAnyHost verifies that wildcard / non-loopback binds (where
// the operator has opted into wide network access) bypass the Host gate but
// still enforce Origin-based CORS. A same-origin call to a LAN IP succeeds;
// a cross-origin call from a foreign page to the same LAN IP is denied.
func TestCORS_PermitAnyHost(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CORS(CORSConfig{
		AllowedOrigins: []string{"http://192.168.1.5:1234"},
		PermitAnyHost:  true,
	})(handler)

	// Same-origin via the bind hostname: Host and Origin agree and Origin
	// is in the allow-list.
	req := httptest.NewRequest("POST", "http://192.168.1.5:1234/api", nil)
	req.Host = "192.168.1.5:1234"
	req.Header.Set("Origin", "http://192.168.1.5:1234")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "same-origin LAN request should be allowed")

	// Cross-origin: bad Origin, regardless of Host.
	called := false
	wrapped2 := CORS(CORSConfig{PermitAnyHost: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req2 := httptest.NewRequest("POST", "http://192.168.1.5:1234/api", nil)
	req2.Host = "192.168.1.5:1234"
	req2.Header.Set("Origin", "http://evil.example.com")
	rec2 := httptest.NewRecorder()
	wrapped2.ServeHTTP(rec2, req2)
	assert.False(t, called)
	assert.Equal(t, http.StatusForbidden, rec2.Code)
}

// TestCORS_WildcardBindSameOriginWithoutAllowlist covers the finding case:
// host: "0.0.0.0" with no public_url / allowed_origins. A browser loaded
// from the LAN IP must be able to make Origin-bearing requests back to the
// same address even though that origin isn't in any explicit list. (This is
// the same-origin-via-Host shortcut that PermitAnyHost enables.)
func TestCORS_WildcardBindSameOriginWithoutAllowlist(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CORS(CORSConfig{PermitAnyHost: true})(handler)

	req := httptest.NewRequest("POST", "http://192.168.1.5:1234/api/v1/workflows/build/run", nil)
	req.Host = "192.168.1.5:1234"
	req.Header.Set("Origin", "http://192.168.1.5:1234")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.True(t, called, "same-origin POST from the bound LAN address should reach the handler")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://192.168.1.5:1234", rec.Header().Get("Access-Control-Allow-Origin"))
}

// TestCORS_PermitAnyHostSchemeMismatch verifies that even with the
// wildcard-bind same-origin shortcut, an http Origin can't claim the https
// scheme (or vice-versa) — scheme is derived from r.TLS and not bypassed.
func TestCORS_PermitAnyHostSchemeMismatch(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	wrapped := CORS(CORSConfig{PermitAnyHost: true})(handler)

	// r.TLS is nil (plain http), so an https Origin must not be accepted.
	req := httptest.NewRequest("POST", "http://192.168.1.5:1234/api", nil)
	req.Host = "192.168.1.5:1234"
	req.Header.Set("Origin", "https://192.168.1.5:1234")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestCORS_SchemeDowngradeRejected verifies scheme is part of the origin
// match: an http Origin must not be accepted when only the https variant is
// in the allow-list.
func TestCORS_SchemeDowngradeRejected(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	wrapped := CORS(CORSConfig{AllowedOrigins: []string{"https://trellis.example.com"}})(handler)

	req := httptest.NewRequest("POST", "https://trellis.example.com/api/v1/foo", nil)
	req.Host = "trellis.example.com"
	req.Header.Set("Origin", "http://trellis.example.com")
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	assert.False(t, called, "http Origin must not satisfy https allow-list entry")
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestResponseWriter_Write(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: rec,
		status:         http.StatusOK,
	}

	n, err := rw.Write([]byte("hello"))
	assert.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, 5, rw.size)
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{
		ResponseWriter: rec,
		status:         http.StatusOK,
	}

	rw.WriteHeader(http.StatusCreated)
	assert.Equal(t, http.StatusCreated, rw.status)
	assert.Equal(t, http.StatusCreated, rec.Code)
}
