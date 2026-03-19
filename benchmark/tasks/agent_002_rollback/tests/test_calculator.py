import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from calculator import add, subtract, multiply, divide, power

def test_add():
    assert add(2, 3) == 5
    assert add(-1, 1) == 0

def test_subtract():
    assert subtract(5, 3) == 2
    assert subtract(1, 1) == 0

def test_multiply():
    assert multiply(3, 4) == 12
    assert multiply(-2, 3) == -6

def test_divide_normal():
    assert divide(10, 2) == 5
    assert divide(9, 3) == 3

def test_divide_by_zero():
    """Divide by zero should return None, not raise exception."""
    result = divide(10, 0)
    assert result is None, f"Expected None when dividing by zero, got {result}"

def test_divide_floats():
    assert divide(7.5, 2.5) == 3.0

def test_power():
    assert power(2, 3) == 8
    assert power(5, 0) == 1

def test_type_validation():
    """Test that non-numeric inputs raise TypeError."""
    try:
        add("a", "b")
        assert False, "Should have raised TypeError"
    except TypeError:
        pass

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
