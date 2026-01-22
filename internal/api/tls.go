// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"fmt"
	"os"
)

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
