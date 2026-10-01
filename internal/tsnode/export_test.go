// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package tsnode

// LockReleases reports how many times the node's state directory lock was released.
func LockReleases(r *Registry, hostname string) int {
	return int(r.nodes[hostname].lockReleases.Load())
}

// CleanupNode runs the node's cleanup directly (for idempotence tests).
func CleanupNode(r *Registry, hostname string) { r.nodes[hostname].cleanup() }
