// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerTLSConfig_Disabled(t *testing.T) {
	certPath, keyPath, tlsConfig, err := serverTLSConfig(ServerConfig{})

	require.NoError(t, err)
	assert.Empty(t, certPath)
	assert.Empty(t, keyPath)
	assert.Nil(t, tlsConfig)
}

func TestServerTLSConfig_Tailscale(t *testing.T) {
	certPath, keyPath, tlsConfig, err := serverTLSConfig(ServerConfig{TLSTailscale: true})

	require.NoError(t, err)
	assert.Empty(t, certPath)
	assert.Empty(t, keyPath)
	require.NotNil(t, tlsConfig)
	assert.NotNil(t, tlsConfig.GetCertificate)
	assert.NotNil(t, tlsConfig.GetConfigForClient)
}

func TestServerTLSConfig_StaticCertificate(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(certPath, []byte("certificate"), 0o600))
	require.NoError(t, os.WriteFile(keyPath, []byte("key"), 0o600))

	gotCertPath, gotKeyPath, tlsConfig, err := serverTLSConfig(ServerConfig{
		TLSCert: certPath,
		TLSKey:  keyPath,
	})

	require.NoError(t, err)
	assert.Equal(t, certPath, gotCertPath)
	assert.Equal(t, keyPath, gotKeyPath)
	require.NotNil(t, tlsConfig)
	assert.Nil(t, tlsConfig.GetCertificate)
	assert.NotNil(t, tlsConfig.GetConfigForClient)
}

func TestServerTLSConfig_RejectsMixedModes(t *testing.T) {
	_, _, _, err := serverTLSConfig(ServerConfig{
		TLSTailscale: true,
		TLSCert:      "cert.pem",
		TLSKey:       "key.pem",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}
