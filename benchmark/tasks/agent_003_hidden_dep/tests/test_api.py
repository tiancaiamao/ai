import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from user import User
from api import get_user_display_name, get_user_info

def test_admin_display_name():
    """Test admin user display name."""
    user = User("John", "admin")
    result = get_user_display_name(user)
    assert result == "John (Admin)", f"Expected 'John (Admin)', got '{result}'"

def test_user_display_name():
    """Test regular user display name."""
    user = User("Jane", "user")
    result = get_user_display_name(user)
    assert result == "Jane (User)", f"Expected 'Jane (User)', got '{result}'"

def test_guest_display_name():
    """Test guest user display name."""
    user = User("Bob", "guest")
    result = get_user_display_name(user)
    assert result == "Bob (Guest)", f"Expected 'Bob (Guest)', got '{result}'"

def test_moderator_display_name():
    """Test moderator display name."""
    user = User("Alice", "moderator")
    result = get_user_display_name(user)
    assert result == "Alice (Moderator)", f"Expected 'Alice (Moderator)', got '{result}'"

def test_unknown_role():
    """Test unknown role (no label)."""
    user = User("Charlie", "unknown")
    result = get_user_display_name(user)
    assert result == "Charlie", f"Expected 'Charlie', got '{result}'"

def test_user_info():
    """Test get_user_info function."""
    user = User("Test", "admin")
    info = get_user_info(user)
    assert info["name"] == "Test"
    assert info["role"] == "admin"
    assert info["display_name"] == "Test (Admin)"

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
