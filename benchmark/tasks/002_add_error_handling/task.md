# Task: Add Error Handling

## Description
Both functions in `main.go` lack proper error handling:
1. `Divide` should return an error when dividing by zero
2. `GetUserAge` should return an error when the user doesn't exist

## Requirements
1. Modify `Divide` to return `(float64, error)`
2. Modify `GetUserAge` to return `(int, error)`
3. Update the main function to handle errors appropriately

## Expected Behavior
- `Divide(10, 0)` should return an error
- `GetUserAge(users, "Unknown")` should return an error
- Existing valid calls should still work
