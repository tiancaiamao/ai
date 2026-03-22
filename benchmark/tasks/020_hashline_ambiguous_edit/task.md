# Task: Ambiguous Edit - Multiple Identical Lines

## Description
The file `duplicate_code.go` contains multiple identical functions that need to be updated differently.
Traditional edit tools may struggle to locate the correct instance due to duplicate code.

## Challenge
- There are 3 identical `ProcessItem` functions in different structs
- You need to fix the bug ONLY in `ProcessorB.ProcessItem`
- The bug is the same in all 3 functions, but only one needs to be fixed
- Using traditional text-based editing may accidentally modify the wrong function

## Expected Behavior
- Only `ProcessorB.ProcessItem` should be modified
- `ProcessorA.ProcessItem` and `ProcessorC.ProcessItem` must remain unchanged
- After fix, all 3 processors should work correctly
- Run `go run duplicate_code.go` to verify

## Steps
1. Read the file `duplicate_code.go`
2. Identify which `ProcessItem` belongs to `ProcessorB`
3. Fix the off-by-one bug ONLY in `ProcessorB.ProcessItem` (change `< len(items)` to `<= len(items)`)
4. Verify with `go run duplicate_code.go` - should print "All processors passed!"