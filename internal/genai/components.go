// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package genai

import (
	"path"
	"sort"
	"strings"
)

// maxDerivedComponents caps how many components DeriveComponents emits. The
// list feeds search scoring and display chips; beyond a handful it's noise.
const maxDerivedComponents = 6

// componentDepth is how many leading directory segments (after stripping
// structural scaffolding) name a component — e.g. depth 2 turns
// "internal/api/handlers/x.go" into "api/handlers".
const componentDepth = 2

// skipSegments mark a path as not part of this codebase (dependencies, build
// output, VCS metadata). Any path containing one of these is ignored.
var skipSegments = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	".git":         true,
	".next":        true,
	".venv":        true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	"target":       true,
}

// scaffoldingSegments are structural directory names that carry no component
// meaning of their own. They're stripped from the front of a path (greedily,
// but never the last remaining segment) before naming the component. This is
// the only place "general repo structure" knowledge lives — a handful of
// conventions across Go, JS/TS, Python, Ruby, and JVM layouts.
var scaffoldingSegments = map[string]bool{
	"internal":  true,
	"src":       true,
	"source":    true,
	"sources":   true,
	"pkg":       true,
	"cmd":       true,
	"app":       true,
	"main":      true,
	"java":      true,
	"kotlin":    true,
	"scala":     true,
	"resources": true,
}

// containerSegments are monorepo grouping directories: the segment that
// FOLLOWS one of these is the real component name (the package/app/service).
var containerSegments = map[string]bool{
	"packages":   true,
	"apps":       true,
	"services":   true,
	"modules":    true,
	"libs":       true,
	"lib":        true,
	"plugins":    true,
	"crates":     true,
	"projects":   true,
	"workspaces": true,
}

// DeriveComponents produces component identifiers deterministically from the
// set of file paths a case touched — "the parts of the codebase touched" — so
// the values are grounded in real paths by construction rather than generated
// by a model. The two consumers (archived-case search scoring and the display
// chips) want exactly this.
//
// Each path maps to its containing module: a run of leading scaffolding
// segments (internal, src, pkg, cmd, app, ...) is stripped; a monorepo
// container (packages, apps, services, ...) hands naming to the next segment;
// otherwise the first componentDepth directory segments name the component.
// Components are ranked by how many files map to each (most-touched first),
// capped at maxDerivedComponents, and run through NormalizeComponents.
func DeriveComponents(paths []string) []string {
	counts := make(map[string]int)
	for _, p := range paths {
		if c := componentForPath(p); c != "" {
			counts[c]++
		}
	}
	ranked := make([]string, 0, len(counts))
	for c := range counts {
		ranked = append(ranked, c)
	}
	// Most-touched component first; alphabetical tie-break keeps it stable.
	sort.Slice(ranked, func(i, j int) bool {
		if counts[ranked[i]] != counts[ranked[j]] {
			return counts[ranked[i]] > counts[ranked[j]]
		}
		return ranked[i] < ranked[j]
	})
	if len(ranked) > maxDerivedComponents {
		ranked = ranked[:maxDerivedComponents]
	}
	return NormalizeComponents(ranked)
}

// componentForPath maps a single repo-relative file path to a component
// identifier, or "" if the path has no meaningful component (a root-level file
// or a skipped dependency/build path).
func componentForPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = path.Clean(strings.ReplaceAll(p, "\\", "/"))
	segs := strings.Split(p, "/")

	// Drop dependency / build / VCS paths entirely — they aren't this codebase.
	for _, s := range segs {
		if skipSegments[s] {
			return ""
		}
	}

	// A path with no directory (root-level file) has no component.
	if len(segs) <= 1 {
		return ""
	}
	segs = segs[:len(segs)-1] // drop the filename

	// Greedily strip leading scaffolding, but never the last remaining segment.
	for len(segs) > 1 && scaffoldingSegments[segs[0]] {
		segs = segs[1:]
	}

	// Monorepo container: the following segment is the component name.
	if len(segs) >= 2 && containerSegments[segs[0]] {
		return segs[1]
	}

	// Otherwise the first componentDepth segments name the component.
	n := min(componentDepth, len(segs))
	return strings.Join(segs[:n], "/")
}
