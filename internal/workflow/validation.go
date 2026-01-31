// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package workflow

import (
	"fmt"
	"regexp"
	"strings"
)

// ValidationError represents one or more input validation failures.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0]
	}
	return fmt.Sprintf("validation failed: %s", strings.Join(e.Errors, "; "))
}

// ValidateInputs validates workflow inputs against their constraints.
// Returns nil if all inputs are valid, or a ValidationError describing all failures.
func ValidateInputs(inputs []WorkflowInput, values map[string]any) error {
	var errors []string

	for _, input := range inputs {
		value, exists := values[input.Name]

		// Check required
		if input.Required {
			if !exists || isEmpty(value) {
				label := input.Label
				if label == "" {
					label = input.Name
				}
				errors = append(errors, fmt.Sprintf("input %q is required", label))
				continue
			}
		}

		// Skip further validation if value not provided
		if !exists || isEmpty(value) {
			continue
		}

		// Convert value to string for pattern/allowed_values validation
		strValue := toString(value)

		// Check allowed_values
		if len(input.AllowedValues) > 0 {
			found := false
			for _, allowed := range input.AllowedValues {
				if strValue == allowed {
					found = true
					break
				}
			}
			if !found {
				errors = append(errors, fmt.Sprintf("input %q value %q is not allowed (allowed: %s)",
					input.Name, strValue, strings.Join(input.AllowedValues, ", ")))
			}
		}

		// Check pattern
		if input.Pattern != "" {
			re, err := regexp.Compile(input.Pattern)
			if err != nil {
				errors = append(errors, fmt.Sprintf("input %q has invalid pattern %q: %v",
					input.Name, input.Pattern, err))
			} else if !re.MatchString(strValue) {
				errors = append(errors, fmt.Sprintf("input %q value %q does not match pattern %s",
					input.Name, strValue, input.Pattern))
			}
		}

		// Type-specific validation
		switch input.Type {
		case "datepicker":
			// Validate date format (YYYY-MM-DD)
			if !isValidDate(strValue) {
				errors = append(errors, fmt.Sprintf("input %q value %q is not a valid date (expected YYYY-MM-DD)",
					input.Name, strValue))
			}
		}
	}

	if len(errors) > 0 {
		return &ValidationError{Errors: errors}
	}
	return nil
}

// isEmpty checks if a value is considered empty.
func isEmpty(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string:
		return v == ""
	case bool:
		return false // booleans are never "empty"
	default:
		return false
	}
}

// toString converts a value to its string representation.
func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		// JSON numbers come as float64
		if v == float64(int(v)) {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// isValidDate checks if a string is a valid YYYY-MM-DD date.
func isValidDate(s string) bool {
	// Simple regex check for format
	matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}$`, s)
	return matched
}
