# Task: Fix Concurrent Bug

## Description

The code in `setup/main.go` has a race condition. The counter should reach 1000000 after 1000 goroutines each increment 1000 times, but it doesn't.

## Problem

The `Counter` struct is not thread-safe. Multiple goroutines are accessing and modifying `c.value` simultaneously without synchronization.

## Requirements

1. Fix the race condition
2. Keep the same API (`Increment()` and `Value()` methods)
3. The program should consistently print "SUCCESS: Counter is thread-safe!"

## Hint

Consider using `sync.Mutex` or `sync/atomic` package.

## Success Criteria

1. `go build` succeeds
2. `go run main.go` consistently prints "SUCCESS"
3. Run the program multiple times to verify no race condition
