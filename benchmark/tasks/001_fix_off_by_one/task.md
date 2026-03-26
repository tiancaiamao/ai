# Task: Fix Off-by-One Error

## Description
The `SumRange` function in `main.go` has an off-by-one error.
It should return the sum of numbers from 1 to n (inclusive),
but currently it only sums from 1 to n-1.

## Expected Behavior
- `SumRange(5)` should return `15` (1+2+3+4+5)
- `SumRange(10)` should return `55`

## Steps
1. Read the file `main.go`
2. Identify the bug in the loop condition
3. Fix the bug using the edit tool
4. Verify the fix by running `go run main.go`
