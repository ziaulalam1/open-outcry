// Package openoutcry exists for one reason: go:embed cannot reach a parent
// directory, and web/ belongs at the repository root where anyone opening the
// project will find it rather than buried under internal/.
//
// The binary is a single file with the frontend baked in. There is nothing to
// deploy, no static path to configure, and no way to arrive at the venue having
// copied the Go binary but not the JavaScript.
package openoutcry

import "embed"

//go:embed all:web
var Web embed.FS
