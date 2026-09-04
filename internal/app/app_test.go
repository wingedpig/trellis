// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wingedpig/trellis/internal/config"
)

func TestNewTemplateContext_PopulatesProjectAndBinaries(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "groups.io"},
		Worktree: config.WorktreeConfig{
			Binaries: config.BinariesConfig{
				Path: "/home/user/bin/{{.Project.Name}}-{{if .Worktree.Name}}{{.Worktree.Name}}{{else}}main{{end}}",
			},
		},
	}

	ctx := newTemplateContext(cfg, "/src/groups.io", config.WorktreeTemplateData{
		Root:   "/src/groups.io-feature",
		Name:   "groups.io-feature",
		Branch: "feature",
	})

	assert.Equal(t, "groups.io", ctx.Project.Name)
	assert.Equal(t, "/src/groups.io", ctx.Project.Root)
	assert.Equal(t, "/src/groups.io-feature", ctx.Worktree.Root)
	assert.Equal(t, "groups.io-feature", ctx.Worktree.Name)
	assert.Equal(t, "feature", ctx.Worktree.Branch)
	assert.Equal(t, "/home/user/bin/groups.io-groups.io-feature", ctx.Worktree.Binaries)
}

// Project values do not depend on a worktree, so they must be present even
// when none is active. The binaries template then sees empty worktree fields.
func TestNewTemplateContext_NoActiveWorktree(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "proj"},
		Worktree: config.WorktreeConfig{
			Binaries: config.BinariesConfig{
				Path: "/bin/{{if .Worktree.Name}}{{.Worktree.Name}}{{else}}{{.Project.Name}}{{end}}",
			},
		},
	}

	ctx := newTemplateContext(cfg, "/src/proj", config.WorktreeTemplateData{})

	assert.Equal(t, "proj", ctx.Project.Name)
	assert.Equal(t, "/src/proj", ctx.Project.Root)
	assert.Equal(t, "/bin/proj", ctx.Worktree.Binaries)
}

func TestNewTemplateContext_BinariesUnsetOrInvalid(t *testing.T) {
	unset := &config.Config{Project: config.ProjectConfig{Name: "proj"}}
	ctx := newTemplateContext(unset, "/src/proj", config.WorktreeTemplateData{Name: "proj-x"})
	assert.Equal(t, "", ctx.Worktree.Binaries)

	invalid := &config.Config{
		Project:  config.ProjectConfig{Name: "proj"},
		Worktree: config.WorktreeConfig{Binaries: config.BinariesConfig{Path: "/bin/{{.Worktree.Name"}},
	}
	ctx = newTemplateContext(invalid, "/src/proj", config.WorktreeTemplateData{Name: "proj-x"})
	assert.Equal(t, "", ctx.Worktree.Binaries, "a template that fails to expand must not leak into the path")
	assert.Equal(t, "proj", ctx.Project.Name)
}

// The values the app puts in the context must reach service configs through
// ExpandConfig, since that is where {{.Project.*}} and {{.Worktree.Binaries}}
// are actually consumed.
func TestNewTemplateContext_FlowsIntoExpandConfig(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "proj"},
		Worktree: config.WorktreeConfig{
			Binaries: config.BinariesConfig{Path: "/bin/{{.Project.Name}}-{{.Worktree.Name}}"},
		},
		Services: []config.ServiceConfig{{
			Name:    "api",
			Command: []string{"{{.Worktree.Binaries}}/api", "--root", "{{.Project.Root}}"},
		}},
	}

	ctx := newTemplateContext(cfg, "/src/proj", config.WorktreeTemplateData{
		Root: "/src/proj-feature", Name: "proj-feature", Branch: "feature",
	})
	expanded, err := config.NewTemplateExpander().ExpandConfig(cfg, ctx)
	require.NoError(t, err)
	require.Len(t, expanded.Services, 1)
	assert.Equal(t, []string{"/bin/proj-proj-feature/api", "--root", "/src/proj"}, expanded.Services[0].Command)
}
