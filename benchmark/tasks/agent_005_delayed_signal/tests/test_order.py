import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from order_processor import OrderProcessor

def test_order_no_discount():
    """Test order without discount."""
    processor = OrderProcessor()
    items = [{"name": "Widget", "price": 50.0, "quantity": 2}]
    result = processor.process_order(items, discount_percent=0)
    
    assert result["subtotal"] == 100.0
    assert result["discount_percent"] == 0
    assert result["discounted_subtotal"] == 100.0
    assert result["tax"] == 8.0
    assert result["total"] == 108.0

def test_order_with_10_percent_discount():
    """Test order with 10% discount.
    
    Expected calculation:
    - Subtotal: $100.00
    - Discount (10%): $10.00
    - Discounted subtotal: $90.00
    - Tax (8%): $7.20
    - Total: $97.20
    """
    processor = OrderProcessor()
    items = [{"name": "Widget", "price": 50.0, "quantity": 2}]
    result = processor.process_order(items, discount_percent=10)
    
    assert result["subtotal"] == 100.0
    assert result["discounted_subtotal"] == 90.0, f"Expected $90.00, got ${result['discounted_subtotal']}"
    assert result["tax"] == 7.20, f"Expected $7.20 tax, got ${result['tax']}"
    assert result["total"] == 97.20, f"Expected $97.20 total, got ${result['total']}"

def test_order_with_small_discount():
    """Test order with small discount that shouldn't round to 0."""
    processor = OrderProcessor()
    items = [{"name": "Item", "price": 10.0, "quantity": 1}]
    result = processor.process_order(items, discount_percent=5)
    
    # 5% of $10 = $0.50, should NOT round to $0 or $1
    assert result["discounted_subtotal"] == 9.50, f"Expected $9.50, got ${result['discounted_subtotal']}"

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
