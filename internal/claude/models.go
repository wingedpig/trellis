// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package claude

// ModelOption is a model alias and optional effort offered by the picker.
type ModelOption struct {
	Model  string `json:"model"`
	Effort string `json:"effort,omitempty"`
	Label  string `json:"label"`
}

// ModelOptions follows Claude Code's effort support for current model aliases.
// Haiku has no effort control. The CLI clamps levels for older provider models
// and organization limits: https://code.claude.com/docs/en/model-config.
var ModelOptions = func() []ModelOption {
	var options []ModelOption
	for _, model := range []struct{ alias, label string }{
		{"opus", "Opus"}, {"sonnet", "Sonnet"}, {"haiku", "Haiku"}, {"fable", "Fable"},
	} {
		options = append(options, ModelOption{Model: model.alias, Label: model.label})
		if model.alias == "haiku" {
			continue
		}
		for _, effort := range []struct{ value, label string }{
			{"low", "Low"}, {"medium", "Medium"}, {"high", "High"},
			{"xhigh", "Extra High"}, {"max", "Max"},
		} {
			options = append(options, ModelOption{
				Model: model.alias, Effort: effort.value, Label: model.label + " " + effort.label,
			})
		}
	}
	return options
}()

func IsValidModelOption(model, effort string) bool {
	if model == "" && effort == "" {
		return true
	}
	for _, option := range ModelOptions {
		if option.Model == model && option.Effort == effort {
			return true
		}
	}
	return false
}
