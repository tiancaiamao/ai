import sys
import os
ROOT_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SETUP_DIR = os.path.join(ROOT_DIR, "setup")
sys.path.insert(0, SETUP_DIR)

from config_parser import parse_database_config

def test_database_config_defaults():
    """Test database config with defaults."""
    config = {}
    result = parse_database_config(config)
    
    assert result["host"] == "localhost"
    assert result["port"] == 5432, f"Expected port 5432, got {result['port']}"
    assert result["user"] == "postgres"
    assert result["database"] == "app"

def test_database_config_custom():
    """Test database config with custom values."""
    config = {
        "database": {
            "host": "db.example.com",
            "port": 3306,
            "user": "admin",
            "password": "secret",
            "database": "myapp"
        }
    }
    result = parse_database_config(config)
    
    assert result["host"] == "db.example.com"
    assert result["port"] == 3306
    assert result["user"] == "admin"
    assert result["password"] == "secret"
    assert result["database"] == "myapp"

def test_database_config_partial():
    """Test database config with partial values."""
    config = {
        "database": {
            "host": "localhost",
            "database": "testdb"
        }
    }
    result = parse_database_config(config)
    
    assert result["host"] == "localhost"
    assert result["port"] == 5432  # Should use default
    assert result["database"] == "testdb"

if __name__ == "__main__":
    import pytest
    pytest.main([__file__, "-v"])
