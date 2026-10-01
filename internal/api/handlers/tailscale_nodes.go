// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"net/http"

	"github.com/wingedpig/trellis/internal/tsnode"
)

// TailscaleNodesHandler reports the status of embedded Tailscale nodes.
type TailscaleNodesHandler struct {
	registry *tsnode.Registry
}

// NewTailscaleNodesHandler creates a handler; registry may be nil.
func NewTailscaleNodesHandler(registry *tsnode.Registry) *TailscaleNodesHandler {
	return &TailscaleNodesHandler{registry: registry}
}

// List returns the status of every started node.
// GET /api/v1/tailscale-nodes
func (h *TailscaleNodesHandler) List(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{"data": h.registry.Status()})
}
