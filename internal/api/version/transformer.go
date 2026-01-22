// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package version

// Transformer is a function that transforms response data for a specific
// API version. It receives the current response data and returns the
// transformed data appropriate for the requested version.
//
// Transformers are used to maintain backwards compatibility when making
// breaking changes. For example, if a field is renamed from "State" to
// "Phase", a transformer for the old version would map "Phase" back to
// "State" so old clients continue working.
type Transformer func(data interface{}) interface{}

// transformers maps versions to endpoint-specific transformers.
// Format: version -> endpoint -> transformer function
//
// Example:
//
//	var transformers = map[string]map[string]Transformer{
//	    "2026-01-17": {
//	        "services.get": func(data interface{}) interface{} {
//	            // Transform new response format to old format
//	            return data
//	        },
//	    },
//	}
//
// Currently empty since 2026-01-17 is the initial version.
var transformers = map[string]map[string]Transformer{}

// Transform applies version-specific transformations to response data.
// If no transformer exists for the version/endpoint combination, the
// data is returned unchanged.
//
// Parameters:
//   - version: The API version from the request (e.g., "2026-01-17")
//   - endpoint: The endpoint identifier (e.g., "services.get", "worktrees.list")
//   - data: The response data to potentially transform
//
// Returns the transformed data.
func Transform(version, endpoint string, data interface{}) interface{} {
	if version == LatestVersion {
		// No transformation needed for latest version
		return data
	}

	versionTransformers, ok := transformers[version]
	if !ok {
		// Unknown version, return data unchanged
		return data
	}

	transformer, ok := versionTransformers[endpoint]
	if !ok {
		// No transformer for this endpoint in this version
		return data
	}

	return transformer(data)
}

// RegisterTransformer adds a transformer for a specific version and endpoint.
// This is typically called during init() to register transformers.
//
// Example:
//
//	func init() {
//	    RegisterTransformer("2026-01-17", "services.get", transformServiceV20260117)
//	}
func RegisterTransformer(version, endpoint string, t Transformer) {
	if transformers[version] == nil {
		transformers[version] = make(map[string]Transformer)
	}
	transformers[version][endpoint] = t
}
