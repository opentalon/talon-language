//go:build tools

// This file is never compiled into any binary. It exists solely so that
// `go mod tidy` records the go.sum checksums for the transitive dependencies
// of github.com/opentalon/talon-db/cmd/talondb-server, which the crash-recovery
// integration test (crash_test.go) builds straight from the talon-db module.
//
// talon-db is consumed as a normal GitHub dependency (no local `replace`), so
// the server binary is built from the module cache and its dependencies must be
// resolvable from this module's go.sum. The server imports these two libraries
// that nothing else in talon-language does; blank-importing them here keeps
// their checksums pinned without pulling them into any real build.
package talondb

import (
	_ "github.com/grpc-ecosystem/go-grpc-prometheus"
	_ "github.com/prometheus/client_golang/prometheus/promhttp"
)
