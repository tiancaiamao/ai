// Package e2e exercises the ai binary as a black box over its public JSON-RPC
// interface. All *_test.go files carry the `e2e` build tag (opt-in, NOT part
// of `make test` / CI): TestMain builds an instrumented binary (`go build
// -cover`) and each test spawns a fresh `ai acp` subprocess, drives it over
// stdin/stdout, then lets it exit so its coverage counters flush to
// GOCOVERDIR. At the end all profiles are merged and the whole-app coverage
// is reported.
//
// Tests skip cleanly when no reachable model endpoint is configured (see
// requireEndpoint), so machines without a running model just report SKIP.
package e2e
