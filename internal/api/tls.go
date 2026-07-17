// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"crypto/tls"
	"fmt"
	"log"
	"os"

	"github.com/tailscale/tscert"
)

// serverTLSConfig validates the API server's TLS settings and returns the
// certificate paths and tls.Config that http.Server should use. Empty paths
// are intentional for Tailscale TLS: GetCertificate obtains and renews the
// certificate through the local Tailscale daemon during the handshake.
func serverTLSConfig(cfg ServerConfig) (certPath, keyPath string, tlsConfig *tls.Config, err error) {
	hasCertKey := cfg.TLSCert != "" || cfg.TLSKey != ""
	if cfg.TLSTailscale && hasCertKey {
		return "", "", nil, fmt.Errorf("tls_tailscale and tls_cert/tls_key are mutually exclusive")
	}

	tlsEnabled, err := CheckTLSConfig(cfg.TLSCert, cfg.TLSKey)
	if err != nil {
		return "", "", nil, err
	}
	if !cfg.TLSTailscale && !tlsEnabled {
		return "", "", nil, nil
	}

	tlsConfig = &tls.Config{
		GetConfigForClient: func(hi *tls.ClientHelloInfo) (*tls.Config, error) {
			// Log every TLS ClientHello so handshake errors can be correlated
			// to a specific client. The stdlib handshake error that follows on
			// a failure shares the same remote address.
			log.Printf("TLS ClientHello from %s: sni=%q alpn=%v versions=%v",
				hi.Conn.RemoteAddr(), hi.ServerName, hi.SupportedProtos, tlsVersionNames(hi.SupportedVersions))
			return nil, nil
		},
	}
	if cfg.TLSTailscale {
		tlsConfig.GetCertificate = tscert.GetCertificate
		return "", "", tlsConfig, nil
	}

	return expandPath(cfg.TLSCert), expandPath(cfg.TLSKey), tlsConfig, nil
}

// tlsVersionNames maps the raw uint16 TLS version codes from a ClientHello
// to short human-readable strings ("1.2", "1.3", ...). Unknown versions are
// rendered as their hex value.
func tlsVersionNames(vs []uint16) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		switch v {
		case tls.VersionTLS10:
			out = append(out, "1.0")
		case tls.VersionTLS11:
			out = append(out, "1.1")
		case tls.VersionTLS12:
			out = append(out, "1.2")
		case tls.VersionTLS13:
			out = append(out, "1.3")
		default:
			out = append(out, fmt.Sprintf("0x%04x", v))
		}
	}
	return out
}

// CheckTLSConfig validates TLS configuration and returns whether TLS should be enabled.
// Returns an error if configuration is invalid.
func CheckTLSConfig(certPath, keyPath string) (bool, error) {
	// Neither specified - no TLS
	if certPath == "" && keyPath == "" {
		return false, nil
	}

	// Only one specified - invalid config
	if certPath == "" || keyPath == "" {
		return false, fmt.Errorf("both tls_cert and tls_key must be specified (got cert=%q, key=%q)", certPath, keyPath)
	}

	// Expand ~ in paths
	certPath = expandPath(certPath)
	keyPath = expandPath(keyPath)

	// Check if both files exist
	if !fileExists(certPath) {
		return false, fmt.Errorf("tls_cert file not found: %s", certPath)
	}
	if !fileExists(keyPath) {
		return false, fmt.Errorf("tls_key file not found: %s", keyPath)
	}

	return true, nil
}

func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
