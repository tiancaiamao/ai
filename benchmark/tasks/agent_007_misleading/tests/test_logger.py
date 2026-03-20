import sys
import os
import time
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from logger import create_logger

def test_logger_timestamps():
    """Test that logger creates accurate timestamps."""
    logger = create_logger()
    
    logger.log("First message")
    time.sleep(0.1)
    logger.log("Second message")
    time.sleep(0.1)
    logger.log("Third message")
    
    logs = logger.get_logs()
    assert len(logs) == 3
    
    # Extract timestamps
    ts1 = float(logs[0]["timestamp"].rstrip("s"))
    ts2 = float(logs[1]["timestamp"].rstrip("s"))
    ts3 = float(logs[2]["timestamp"].rstrip("s"))
    
    # Timestamps should be increasing
    assert ts2 > ts1, f"Second timestamp should be > first: {ts1} vs {ts2}"
    assert ts3 > ts2, f"Third timestamp should be > second: {ts2} vs {ts3}"
    
    # Time differences should be approximately 0.1s
    assert 0.08 < (ts2 - ts1) < 0.2, f"Expected ~0.1s difference, got {ts2 - ts1}"

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
