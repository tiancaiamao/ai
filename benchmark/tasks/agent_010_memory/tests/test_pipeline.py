import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from stage1_load import load_data
from stage5_save import save_all, run_pipeline

def test_amount_formatting():
    """Test that amounts are formatted correctly as dollars, not cents."""
    data = [{"date": "2024-01-15", "amount": 10000}]  # 10000 cents = $100.00
    result = save_all(data)
    
    # Should be "USD 100.00" not "USD 10000.00"
    assert result[0]["amount"] == "USD 100.00", f"Expected 'USD 100.00', got '{result[0]['amount']}'"

def test_full_pipeline():
    """Test complete pipeline."""
    data = load_data("dummy_source")
    result = run_pipeline(data)
    
    assert len(result) == 2
    
    # First record: 10000 cents = $100.00
    assert result[0]["amount"] == "USD 100.00", f"Got {result[0]['amount']}"
    
    # Second record: 25000 cents = $250.00
    assert result[1]["amount"] == "USD 250.00", f"Got {result[1]['amount']}"

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
