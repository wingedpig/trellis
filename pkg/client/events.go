// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// EventClient provides access to the Trellis event log.
//
// Events track system activity such as service starts/stops, worktree switches,
// workflow executions, and other notable occurrences.
//
// Access this client through [Client.Events]:
//
//	events, err := client.Events.List(ctx, &client.ListOptions{Limit: 50})
type EventClient struct {
	c *Client
}

// ListOptions configures event listing.
type ListOptions struct {
	// Limit is the maximum number of events to return.
	Limit int

	// Types filters to only these event types (e.g., "service.started").
	Types []string

	// Worktree filters to events from this worktree.
	Worktree string

	// Since filters to events after this time.
	Since time.Time

	// Until filters to events before this time.
	Until time.Time
}

// List returns recent events from the event log.
//
// Events are returned in reverse chronological order (newest first).
func (e *EventClient) List(ctx context.Context, opts *ListOptions) ([]Event, error) {
	path := "/api/v1/events"

	if opts != nil {
		params := url.Values{}
		if opts.Limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", opts.Limit))
		}
		for _, t := range opts.Types {
			params.Add("type", t)
		}
		if opts.Worktree != "" {
			params.Set("worktree", opts.Worktree)
		}
		if !opts.Since.IsZero() {
			params.Set("since", opts.Since.Format(time.RFC3339))
		}
		if !opts.Until.IsZero() {
			params.Set("until", opts.Until.Format(time.RFC3339))
		}
		if len(params) > 0 {
			path += "?" + params.Encode()
		}
	}

	data, err := e.c.get(ctx, path)
	if err != nil {
		return nil, err
	}

	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("failed to parse events: %w", err)
	}

	return events, nil
}
