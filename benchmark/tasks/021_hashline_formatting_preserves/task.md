# Task: Edit After File is Reformatted

## Description
The file `poorly_formatted.go` needs bug fixes, but the file has been reformatted with different indentation and spacing.
This tests whether the edit tool can still locate and modify the correct code after formatting changes.

## Challenge
- The file has poor formatting (inconsistent indentation)
- You need to fix bugs in specific functions
- Traditional fuzzy matching may fail if indentation or spacing has changed
- Hashline mode should work regardless of formatting

## Expected Behavior
- Fix the bug in `CalculateTotal` function (change `price * quantity - discount` to `price * quantity + discount`)
- Fix the bug in `GetTaxRate` function (change `return 0.08` to `return 0.10`)
- Run `go run poorly_formatted.go` to verify - should print "All calculations passed!"

## Steps
1. Read the file `poorly_formatted.go`
2. Identify and fix both bugs
3. Verify with `go run poorly_formatted.go`