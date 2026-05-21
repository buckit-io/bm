// Package admin is a thin wrapper around buckit-io/madmin-go/v3. It exists so
// the rest of bm imports a small domain-typed surface — `ServerInfo`,
// `AccountInfo`, and (future) the operation-flavoured methods — without ever
// touching madmin types directly. That isolation makes madmin version skew a
// one-package problem.
//
// The wrapper also keeps a per-cluster client cache so callers that issue
// repeated calls (refresh, executors) don't pay handshake cost each time.
package admin
