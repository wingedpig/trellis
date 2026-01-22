// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package client provides a Go client library for the Trellis API.
//
// Trellis is a service manager and development environment orchestrator.
// This client library provides typed access to all Trellis API endpoints,
// allowing you to manage services, worktrees, workflows, logs, and more.
//
// # Getting Started
//
// Create a client pointing to your Trellis server:
//
//	c := client.New("http://localhost:8080")
//
// The client provides access to different API resources through sub-clients:
//
//	// List all services
//	services, err := c.Services.List(ctx)
//
//	// Start a service
//	svc, err := c.Services.Start(ctx, "backend")
//
//	// List worktrees
//	worktrees, err := c.Worktrees.List(ctx)
//
//	// Run a workflow
//	status, err := c.Workflows.Run(ctx, "build", nil)
//
// # API Versioning
//
// Trellis uses Stripe-style date-based API versioning. By default, the client
// uses the latest API version. You can pin to a specific version for stability:
//
//	c := client.New("http://localhost:8080", client.WithVersion("2026-01-17"))
//
// The version is sent via the Trellis-Version HTTP header on each request.
//
// # Configuration Options
//
// The client can be configured with functional options:
//
//	c := client.New("http://localhost:8080",
//	    client.WithVersion("2026-01-17"),
//	    client.WithTimeout(60 * time.Second),
//	    client.WithHTTPClient(customHTTPClient),
//	)
//
// # Error Handling
//
// API errors are returned as *APIError values, which include an error code
// and message:
//
//	svc, err := c.Services.Get(ctx, "unknown")
//	if err != nil {
//	    if apiErr, ok := err.(*client.APIError); ok {
//	        fmt.Printf("API error: %s - %s\n", apiErr.Code, apiErr.Message)
//	    }
//	}
//
// # Context Support
//
// All API methods accept a context.Context for cancellation and timeouts:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	services, err := c.Services.List(ctx)
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a Trellis API client.
//
// A Client provides access to the Trellis API through resource-specific
// sub-clients. Use [New] to create a Client instance.
//
// The Client is safe for concurrent use by multiple goroutines.
type Client struct {
	baseURL    string
	version    string
	httpClient *http.Client

	// Services provides access to service management operations.
	// Services are long-running processes managed by Trellis.
	Services *ServiceClient

	// Worktrees provides access to git worktree operations.
	// Worktrees allow switching between different branches/checkouts.
	Worktrees *WorktreeClient

	// Workflows provides access to workflow execution.
	// Workflows are predefined command sequences (build, test, deploy, etc.).
	Workflows *WorkflowClient

	// Events provides access to the event log.
	// Events track system activity like service starts, worktree switches, etc.
	Events *EventClient

	// Logs provides access to log viewer operations.
	// Log viewers aggregate logs from external sources like system logs.
	Logs *LogClient

	// Trace provides access to distributed tracing operations.
	// Traces correlate log entries across multiple services by ID.
	Trace *TraceClient

	// Notify provides access to notification operations.
	// Notifications can trigger alerts, sounds, or other actions.
	Notify *NotifyClient

	// Crashes provides access to crash history operations.
	// Crashes store context from service crashes for debugging.
	Crashes *CrashClient
}

// Option configures a [Client]. Options are passed to [New] to customize
// client behavior.
type Option func(*Client)

// New creates a new Trellis API client with the given base URL and options.
//
// The baseURL should be the root URL of the Trellis server (e.g., "http://localhost:8080").
// Any trailing slash is automatically removed.
//
// By default, the client uses:
//   - The latest API version ([LatestVersion])
//   - A 30-second HTTP timeout
//
// Use options like [WithVersion], [WithTimeout], or [WithHTTPClient] to customize.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		version: LatestVersion,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	// Initialize service clients
	c.Services = &ServiceClient{c: c}
	c.Worktrees = &WorktreeClient{c: c}
	c.Workflows = &WorkflowClient{c: c}
	c.Events = &EventClient{c: c}
	c.Logs = &LogClient{c: c}
	c.Trace = &TraceClient{c: c}
	c.Notify = &NotifyClient{c: c}
	c.Crashes = &CrashClient{c: c}

	return c
}

// WithVersion sets the API version to use for all requests.
//
// Trellis uses Stripe-style date-based versioning (e.g., "2026-01-17").
// Pinning to a specific version ensures API compatibility as the server evolves.
// See the version constants ([LatestVersion], [Version20260117]) for available versions.
func WithVersion(v string) Option {
	return func(c *Client) {
		c.version = v
	}
}

// WithHTTPClient sets a custom HTTP client for making requests.
//
// This is useful for advanced configurations like custom TLS settings,
// proxy configuration, or request tracing.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// WithTimeout sets the HTTP client timeout for all requests.
//
// The default timeout is 30 seconds. Use a longer timeout for operations
// that may take more time, such as workflow execution.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.httpClient.Timeout = d
	}
}

// Version returns the API version being used.
func (c *Client) Version() string {
	return c.version
}

// BaseURL returns the base URL of the API.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// apiResponse is the standard API response envelope.
type apiResponse struct {
	Data  json.RawMessage `json:"data"`
	Error *APIError       `json:"error"`
}

// APIError represents an error response from the Trellis API.
//
// API errors include a machine-readable Code and a human-readable Message.
// Some errors may include additional Details for debugging.
//
// Common error codes include:
//   - "not_found": The requested resource does not exist
//   - "invalid_request": The request was malformed or invalid
//   - "conflict": The operation conflicts with current state
//   - "internal_error": An unexpected server error occurred
type APIError struct {
	// Code is a machine-readable error code (e.g., "not_found", "invalid_request").
	Code string `json:"code"`

	// Message is a human-readable description of the error.
	Message string `json:"message"`

	// Details contains additional error information, if available.
	Details map[string]interface{} `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Message
}

// get performs a GET request to the given path.
func (c *Client) get(ctx context.Context, path string) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

// post performs a POST request to the given path with no body.
func (c *Client) post(ctx context.Context, path string) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPost, path, nil)
}

// postJSON performs a POST request with a JSON body.
func (c *Client) postJSON(ctx context.Context, path string, body interface{}) (json.RawMessage, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	return c.do(ctx, http.MethodPost, path, bytes.NewReader(data))
}

// delete performs a DELETE request to the given path.
func (c *Client) delete(ctx context.Context, path string) (json.RawMessage, error) {
	return c.do(ctx, http.MethodDelete, path, nil)
}

// do performs an HTTP request and parses the response.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (json.RawMessage, error) {
	url := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Trellis-Version", c.version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	return c.parseResponse(resp)
}

// parseResponse reads and parses an API response.
func (c *Client) parseResponse(resp *http.Response) (json.RawMessage, error) {
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Try to parse as standard envelope
	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		// If we can't parse it and status is bad, return error
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(respBody))
		}
		// Return raw body for non-envelope responses
		return respBody, nil
	}

	// Check for error in envelope
	if apiResp.Error != nil {
		return nil, apiResp.Error
	}

	// Check for error embedded in data (some endpoints do this)
	if resp.StatusCode >= 400 {
		var errData APIError
		if err := json.Unmarshal(apiResp.Data, &errData); err == nil && errData.Code != "" {
			return nil, &errData
		}
	}

	return apiResp.Data, nil
}
