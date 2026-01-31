// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateInputs_Required(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "name", Type: "text", Required: true},
	}

	// Missing required input
	err := ValidateInputs(inputs, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")

	// Empty required input
	err = ValidateInputs(inputs, map[string]any{"name": ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")

	// Valid required input
	err = ValidateInputs(inputs, map[string]any{"name": "value"})
	assert.NoError(t, err)
}

func TestValidateInputs_RequiredWithLabel(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "user_name", Type: "text", Label: "User Name", Required: true},
	}

	err := ValidateInputs(inputs, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "User Name")
}

func TestValidateInputs_Pattern(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "id", Type: "text", Pattern: `^[0-9]+$`},
	}

	// Valid pattern
	err := ValidateInputs(inputs, map[string]any{"id": "12345"})
	assert.NoError(t, err)

	// Invalid pattern
	err = ValidateInputs(inputs, map[string]any{"id": "abc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match pattern")

	// Empty value should be skipped (not required)
	err = ValidateInputs(inputs, map[string]any{"id": ""})
	assert.NoError(t, err)

	// Missing value should be skipped (not required)
	err = ValidateInputs(inputs, map[string]any{})
	assert.NoError(t, err)
}

func TestValidateInputs_PatternWithInjection(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "id", Type: "text", Pattern: `^[0-9]+$`},
	}

	// Attempt command injection
	err := ValidateInputs(inputs, map[string]any{"id": "123; rm -rf /"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match pattern")

	// Another injection attempt
	err = ValidateInputs(inputs, map[string]any{"id": "$(whoami)"})
	require.Error(t, err)
}

func TestValidateInputs_AllowedValues(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "env", Type: "text", AllowedValues: []string{"staging", "production"}},
	}

	// Valid value
	err := ValidateInputs(inputs, map[string]any{"env": "staging"})
	assert.NoError(t, err)

	err = ValidateInputs(inputs, map[string]any{"env": "production"})
	assert.NoError(t, err)

	// Invalid value
	err = ValidateInputs(inputs, map[string]any{"env": "development"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
	assert.Contains(t, err.Error(), "staging, production")

	// Empty value should be skipped (not required)
	err = ValidateInputs(inputs, map[string]any{"env": ""})
	assert.NoError(t, err)
}

func TestValidateInputs_Datepicker(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "date", Type: "datepicker", Required: true},
	}

	// Valid date
	err := ValidateInputs(inputs, map[string]any{"date": "2024-01-15"})
	assert.NoError(t, err)

	// Invalid date format
	err = ValidateInputs(inputs, map[string]any{"date": "01/15/2024"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid date")

	// Invalid date format
	err = ValidateInputs(inputs, map[string]any{"date": "January 15, 2024"})
	require.Error(t, err)
}

func TestValidateInputs_Checkbox(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "dry_run", Type: "checkbox", Required: true},
	}

	// Boolean true
	err := ValidateInputs(inputs, map[string]any{"dry_run": true})
	assert.NoError(t, err)

	// Boolean false (still valid for required - it's a value)
	err = ValidateInputs(inputs, map[string]any{"dry_run": false})
	assert.NoError(t, err)
}

func TestValidateInputs_MultipleErrors(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "id", Type: "text", Pattern: `^[0-9]+$`, Required: true},
		{Name: "env", Type: "text", AllowedValues: []string{"staging", "production"}, Required: true},
	}

	// Both invalid
	err := ValidateInputs(inputs, map[string]any{"id": "abc", "env": "development"})
	require.Error(t, err)

	valErr, ok := err.(*ValidationError)
	require.True(t, ok)
	assert.Len(t, valErr.Errors, 2)
}

func TestValidateInputs_NoInputs(t *testing.T) {
	// No inputs defined
	err := ValidateInputs(nil, map[string]any{"extra": "value"})
	assert.NoError(t, err)

	err = ValidateInputs([]WorkflowInput{}, map[string]any{})
	assert.NoError(t, err)
}

func TestValidateInputs_InvalidPattern(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "id", Type: "text", Pattern: `[invalid`}, // Invalid regex
	}

	err := ValidateInputs(inputs, map[string]any{"id": "value"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid pattern")
}

func TestValidateInputs_NumericValue(t *testing.T) {
	inputs := []WorkflowInput{
		{Name: "count", Type: "text", Pattern: `^[0-9]+$`},
	}

	// JSON numbers come as float64
	err := ValidateInputs(inputs, map[string]any{"count": float64(42)})
	assert.NoError(t, err)

	err = ValidateInputs(inputs, map[string]any{"count": float64(3.14)})
	require.Error(t, err) // 3.14 won't match ^[0-9]+$
}
