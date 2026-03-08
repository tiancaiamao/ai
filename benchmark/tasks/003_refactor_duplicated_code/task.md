# Task: Refactor Duplicated Code

## Description
The code in `main.go` has significant code duplication across three functions:
- `ProcessUser`
- `ProcessProduct`
- `ProcessOrder`

All three functions have nearly identical validation logic patterns.

## Requirements
1. Create a generic validation helper function
2. Refactor the three functions to use the helper
3. Maintain the same public API and behavior
4. The program should produce the same output

## Hints
- Consider using generics or interface{} for the validation helper
- The validation pattern is: check if value is empty/zero, return error message
