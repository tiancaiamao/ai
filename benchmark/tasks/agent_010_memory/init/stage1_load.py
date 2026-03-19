"""Stage 1: Load data from source."""

# IMPORTANT: Data format from source
# - All dates are in format: YYYY-MM-DD
# - All amounts are in cents (not dollars)
# - Currency code is stored in config: DATA_CURRENCY = "USD"

DATA_CURRENCY = "USD"  # Remember this for stage 5!

def load_data(source):
    """Load data from source."""
    # In real implementation, would load from file/API
    return [
        {"date": "2024-01-15", "amount": 10000},  # 100.00 USD
        {"date": "2024-01-16", "amount": 25000},  # 250.00 USD
    ]

def get_currency():
    """Get the currency code for this data."""
    return DATA_CURRENCY