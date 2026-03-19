"""Stage 3: Transform data."""

def transform_amount(amount):
    """Transform amount (no-op in this stage)."""
    # Future: might add currency conversion
    return amount

def transform_date(date_str):
    """Transform date to ISO format."""
    # Already in ISO format, no change
    return date_str

def transform_record(record):
    """Transform a single record."""
    return {
        "date": transform_date(record["date"]),
        "amount": transform_amount(record["amount"])
    }

def transform_all(data):
    """Transform all records."""
    return [transform_record(r) for r in data]

# More noise
class Transformer:
    def __init__(self):
        pass
    
    def transform(self, data):
        return transform_all(data)