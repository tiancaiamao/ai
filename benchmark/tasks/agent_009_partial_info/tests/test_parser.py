import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from csv_parser import parse_csv, parse_csv_line

def test_simple_csv():
    """Test simple CSV without quotes."""
    result = parse_csv("a,b,c")
    assert result == [["a", "b", "c"]]

def test_csv_with_quotes():
    """Test CSV with quoted field containing comma."""
    result = parse_csv('name,"Smith, John",age')
    # Should be 3 fields, not 4
    assert len(result[0]) == 3, f"Expected 3 fields, got {len(result[0])}: {result[0]}"
    assert result[0][1] == "Smith, John", f"Expected 'Smith, John', got {result[0][1]}"

def test_multiline_csv():
    """Test multiline CSV."""
    csv = "a,b,c\n1,2,3\nx,y,z"
    result = parse_csv(csv)
    assert len(result) == 3

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
