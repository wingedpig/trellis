// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// NotifyClient provides access to notification operations.
//
// Notifications can trigger sounds, visual alerts, or other actions
// depending on the Trellis configuration.
//
// Access this client through [Client.Notify]:
//
//	_, err := client.Notify.Send(ctx, "Build complete!", client.NotifyDone)
type NotifyClient struct {
	c *Client
}

// Send sends a notification to the Trellis server.
//
// The notification type determines how the notification is presented:
//   - [NotifyDone]: Task completed successfully (success sound/visual)
//   - [NotifyBlocked]: Waiting for user input (attention-grabbing alert)
//   - [NotifyError]: An error occurred (error sound/alert)
func (n *NotifyClient) Send(ctx context.Context, message string, notifyType NotifyType) (*NotifyResponse, error) {
	req := NotifyRequest{
		Message: message,
		Type:    string(notifyType),
	}

	data, err := n.c.postJSON(ctx, "/api/v1/notify", req)
	if err != nil {
		return nil, err
	}

	var resp NotifyResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse notify response: %w", err)
	}

	return &resp, nil
}
