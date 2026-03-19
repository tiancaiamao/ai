# Task: Fix the Calculator (Mandatory Rollback)

## Description
The `calculator.py` file has a bug in the `divide` function.
The function should handle division by zero, but currently it doesn't.

## Current Behavior
```python
calculator.divide(10, 0)  # Raises ZeroDivisionError
```

## Expected Behavior
```python
calculator.divide(10, 0)  # Should return None (or raise a custom error)
```

## Important Warning
⚠️ **READ THIS CAREFULLY**

The code has a misleading error message. The error you see first is NOT the real problem.
If you fix the first error you see, the tests will fail even worse.

You must:
1. Understand the full error chain
2. Fix the ROOT CAUSE, not the symptom
3. If your first fix makes things worse, REVERT and try again

## Files
- `calculator.py`