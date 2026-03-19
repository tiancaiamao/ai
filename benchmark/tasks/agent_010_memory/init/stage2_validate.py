"""Stage 2: Validate data."""

def validate_date(date_str):
    """Validate date format."""
    if len(date_str) != 10:
        return False
    parts = date_str.split("-")
    if len(parts) != 3:
        return False
    return all(p.isdigit() for p in parts)

def validate_amount(amount):
    """Validate amount is positive."""
    return amount > 0

def validate_record(record):
    """Validate a single record."""
    return validate_date(record.get("date", "")) and validate_amount(record.get("amount", 0))

def validate_all(data):
    """Validate all records."""
    return [r for r in data if validate_record(r)]

# Lots of helper functions to add noise
def helper_1(x): return x
def helper_2(x): return x
def helper_3(x): return x
def helper_4(x): return x
def helper_5(x): return x
def helper_6(x): return x
def helper_7(x): return x
def helper_8(x): return x
def helper_9(x): return x
def helper_10(x): return x