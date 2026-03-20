import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from module_c import custom_sort

def test_custom_sort_ascending():
    """Test that custom_sort returns ascending order."""
    result = custom_sort([5, 2, 8, 1, 9, 3])
    assert result == [1, 2, 3, 5, 8, 9], f"Expected [1, 2, 3, 5, 8, 9], got {result}"

def test_custom_sort_with_duplicates():
    """Test custom_sort with duplicate values."""
    result = custom_sort([3, 1, 4, 1, 5, 9, 2, 6])
    assert result == [1, 1, 2, 3, 4, 5, 6, 9], f"Expected [1, 1, 2, 3, 4, 5, 6, 9], got {result}"

def test_custom_sort_empty():
    """Test custom_sort with empty list."""
    result = custom_sort([])
    assert result == [], f"Expected [], got {result}"

def test_custom_sort_single():
    """Test custom_sort with single element."""
    result = custom_sort([42])
    assert result == [42], f"Expected [42], got {result}"

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
