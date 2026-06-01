//go:build tools
// +build tools

// Package tools pins the versions of build-time-only dependencies (e.g.
// code generators) so that `go mod tidy` keeps them in go.mod / go.sum.
// The `tools` build tag prevents them from being linked into normal builds.
package tools

import (
	_ "go.uber.org/mock/mockgen"
)
