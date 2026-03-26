# Task: Add JSON Error Handling

## Description
The `parseJSON` function doesn't handle errors when given invalid JSON.
The `toJSON` function should handle errors during marshaling.

## Current Behavior
- `parseJSON` silently ignores JSON errors
- `toJSON` silently ignores marshaling errors

## Expected Behavior
- `parseJSON` should return an error for invalid JSON
- `toJSON` should return an error if marshaling fails

## Hints
- Use `(map[string]interface{}, error)` as return type
- Check `json.Valid` for validation
