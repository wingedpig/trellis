// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// TailscaleNodeClient provides access to embedded Tailscale node status.
type TailscaleNodeClient struct {
	c *Client
}

// TailscaleNode is the status of one embedded Tailscale node.
type TailscaleNode struct {
	Hostname         string     `json:"hostname"`
	StateDir         string     `json:"state_dir"`
	State            string     `json:"state"`
	FQDN             string     `json:"fqdn,omitempty"`
	HostnameMismatch bool       `json:"hostname_mismatch"`
	TailscaleIPs     []string   `json:"tailscale_ips,omitempty"`
	LoginURL         string     `json:"login_url,omitempty"`
	KeyExpiry        *time.Time `json:"key_expiry,omitempty"`
	KeyExpiresSoon   bool       `json:"key_expires_soon"`
	CertError        string     `json:"cert_error,omitempty"`
	CertErrorAt      *time.Time `json:"cert_error_at,omitempty"`
	CertNotAfter     *time.Time `json:"cert_not_after,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
}

// List returns the status of every started Tailscale node.
func (t *TailscaleNodeClient) List(ctx context.Context) ([]TailscaleNode, error) {
	data, err := t.c.get(ctx, "/api/v1/tailscale-nodes")
	if err != nil {
		return nil, err
	}
	var nodes []TailscaleNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, fmt.Errorf("failed to parse tailscale nodes: %w", err)
	}
	return nodes, nil
}
