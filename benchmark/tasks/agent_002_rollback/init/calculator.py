"""Simple calculator module."""

def _validate_inputs(a, b):
    """Validate inputs for arithmetic operations."""
    if not isinstance(a, (int, float)) or not isinstance(b, (int, float)):
        raise TypeError("Both arguments must be numbers")
    return a, b

def add(a, b):
    """Add two numbers."""
    _validate_inputs(a, b)
    return a + b

def subtract(a, b):
    """Subtract b from a."""
    _validate_inputs(a, b)
    return a - b

def multiply(a, b):
    """Multiply two numbers."""
    _validate_inputs(a, b)
    return a * b

def divide(a, b):
    """Divide a by b.
    
    Returns None if b is zero.
    """
    _validate_inputs(a, b)
    # BUG: This doesn't handle b == 0
    # But the REAL bug is in _validate_inputs which should reject b == 0
    return a / b

def power(a, b):
    """Raise a to the power of b."""
    _validate_inputs(a, b)
    return a ** b