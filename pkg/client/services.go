// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
)

// ServiceClient provides access to service management operations.
//
// Services are long-running processes supervised by Trellis. The ServiceClient
// allows listing, starting, stopping, and restarting services, as well as
// accessing their logs.
//
// Access this client through [Client.Services]:
//
//	services, err := client.Services.List(ctx)
type ServiceClient struct {
	c *Client
}

// List returns all configured services and their current status.
//
// Example:
//
//	services, err := client.Services.List(ctx)
//	for _, svc := range services {
//	    fmt.Printf("%s: %s\n", svc.Name, svc.Status.State)
//	}
func (s *ServiceClient) List(ctx context.Context) ([]Service, error) {
	data, err := s.c.get(ctx, "/api/v1/services")
	if err != nil {
		return nil, err
	}

	var services []Service
	if err := json.Unmarshal(data, &services); err != nil {
		return nil, fmt.Errorf("failed to parse services: %w", err)
	}

	return services, nil
}

// Get returns a specific service by name.
//
// Returns an error if the service does not exist.
func (s *ServiceClient) Get(ctx context.Context, name string) (*Service, error) {
	data, err := s.c.get(ctx, "/api/v1/services/"+name)
	if err != nil {
		return nil, err
	}

	var svc Service
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, fmt.Errorf("failed to parse service: %w", err)
	}

	return &svc, nil
}

// Start starts a stopped service.
//
// Returns the service with its updated state. If the service is already
// running, this is a no-op and returns the current state.
func (s *ServiceClient) Start(ctx context.Context, name string) (*Service, error) {
	data, err := s.c.post(ctx, "/api/v1/services/"+name+"/start")
	if err != nil {
		return nil, err
	}

	var svc Service
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, fmt.Errorf("failed to parse service: %w", err)
	}

	return &svc, nil
}

// Stop stops a running service.
//
// The service process receives a SIGTERM signal and is given time to shut down
// gracefully. Returns the service with its updated state.
func (s *ServiceClient) Stop(ctx context.Context, name string) (*Service, error) {
	data, err := s.c.post(ctx, "/api/v1/services/"+name+"/stop")
	if err != nil {
		return nil, err
	}

	var svc Service
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, fmt.Errorf("failed to parse service: %w", err)
	}

	return &svc, nil
}

// Restart stops and starts a service.
//
// If the service is running, it is stopped first, then started. If the service
// is already stopped, it is simply started. Returns the service with its updated state.
func (s *ServiceClient) Restart(ctx context.Context, name string) (*Service, error) {
	data, err := s.c.post(ctx, "/api/v1/services/"+name+"/restart")
	if err != nil {
		return nil, err
	}

	var svc Service
	if err := json.Unmarshal(data, &svc); err != nil {
		return nil, fmt.Errorf("failed to parse service: %w", err)
	}

	return &svc, nil
}

// Logs returns the most recent log lines from a service's log buffer.
//
// The lines parameter specifies how many lines to return. The response is
// raw JSON containing the log lines which can be parsed or processed as needed.
//
// For structured log access, consider using the log viewer APIs instead.
func (s *ServiceClient) Logs(ctx context.Context, name string, lines int) ([]byte, error) {
	path := fmt.Sprintf("/api/v1/services/%s/logs?lines=%d", name, lines)
	return s.c.get(ctx, path)
}

// ClearLogs clears the in-memory log buffer for a service.
//
// This removes all buffered log lines. It does not affect log files on disk.
func (s *ServiceClient) ClearLogs(ctx context.Context, name string) error {
	_, err := s.c.delete(ctx, "/api/v1/services/"+name+"/logs")
	return err
}
