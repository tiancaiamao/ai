import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from data_processor import remove_duplicates
import time

def test_remove_duplicates_small():
    """Test with small list."""
    assert remove_duplicates([1, 2, 2, 3, 3, 3]) == [1, 2, 3]

def test_remove_duplicates_performance():
    """Test that remove_duplicates is O(n), not O(n²).
    
    With O(n): 10K items should take < 0.1s
    With O(n²): 10K items would take > 1s
    """
    large_list = list(range(10000)) * 2  # 20K items with duplicates
    
    start = time.time()
    result = remove_duplicates(large_list)
    duration = time.time() - start
    
    # Should complete in < 0.1s if O(n)
    assert duration < 0.1, f"Too slow: {duration}s (expected < 0.1s for O(n))"
    assert len(result) == 10000

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
