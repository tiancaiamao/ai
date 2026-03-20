import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from stats import calculate_median

def test_median_odd():
    """Test median with odd number of elements."""
    assert calculate_median([1, 2, 3, 4, 5]) == 3
    assert calculate_median([5, 1, 3, 2, 4]) == 3

def test_median_even():
    """Test median with even number of elements."""
    result = calculate_median([1, 2, 3, 4])
    assert result == 2.5

def test_median_empty():
    """Test median with empty list."""
    assert calculate_median([]) == 0

def test_median_single():
    """Test median with single element."""
    assert calculate_median([42]) == 42

def test_median_unsorted():
    """Test median with unsorted input."""
    assert calculate_median([5, 1, 3, 2, 4]) == 3

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
