// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package static

import "embed"

//go:embed css/* img/* js/*
var Files embed.FS
