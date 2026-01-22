// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// TraceClient provides access to distributed tracing operations.
//
// Distributed tracing allows correlating log entries across multiple services
// by searching for a common ID (trace ID, request ID, correlation ID, etc.).
//
// Access this client through [Client.Trace]:
//
//	result, err := client.Trace.Execute(ctx, &client.TraceRequest{
//	    ID:    "abc123",
//	    Group: "web",
//	    Start: time.Now().Add(-1*time.Hour),
//	    End:   time.Now(),
//	})
type TraceClient struct {
	c *Client
}

// Execute starts a distributed trace query.
//
// The trace runs asynchronously. This method returns immediately with the
// initial result including the report name. Use [TraceClient.GetReport] to
// poll for completion and retrieve the full results.
func (t *TraceClient) Execute(ctx context.Context, req *TraceRequest) (*TraceResult, error) {
	data, err := t.c.postJSON(ctx, "/api/v1/trace", req)
	if err != nil {
		return nil, err
	}

	var result TraceResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse trace result: %w", err)
	}

	return &result, nil
}

// ListReports returns summaries of all saved trace reports.
//
// Use [TraceClient.GetReport] to retrieve the full report with log entries.
func (t *TraceClient) ListReports(ctx context.Context) ([]TraceReportSummary, error) {
	data, err := t.c.get(ctx, "/api/v1/trace/reports")
	if err != nil {
		return nil, err
	}

	var reports []TraceReportSummary
	if err := json.Unmarshal(data, &reports); err != nil {
		return nil, fmt.Errorf("failed to parse trace reports: %w", err)
	}

	return reports, nil
}

// GetReport returns a complete trace report by name.
//
// The report includes all matching log entries sorted by timestamp.
// Use this to check trace completion status or retrieve results.
func (t *TraceClient) GetReport(ctx context.Context, name string) (*TraceReport, error) {
	data, err := t.c.get(ctx, "/api/v1/trace/reports/"+url.PathEscape(name))
	if err != nil {
		return nil, err
	}

	var report TraceReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("failed to parse trace report: %w", err)
	}

	return &report, nil
}

// DeleteReport permanently removes a trace report.
func (t *TraceClient) DeleteReport(ctx context.Context, name string) error {
	_, err := t.c.delete(ctx, "/api/v1/trace/reports/"+url.PathEscape(name))
	return err
}

// ListGroups returns all configured trace groups.
//
// A trace group defines a set of log viewers to search together.
// Use the group name when calling [TraceClient.Execute].
func (t *TraceClient) ListGroups(ctx context.Context) ([]TraceGroup, error) {
	data, err := t.c.get(ctx, "/api/v1/trace/groups")
	if err != nil {
		return nil, err
	}

	var groups []TraceGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		return nil, fmt.Errorf("failed to parse trace groups: %w", err)
	}

	return groups, nil
}
