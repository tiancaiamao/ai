"""Stage 5: Save data to output."""

from stage1_load import get_currency

def format_amount_for_display(amount_in_cents):
    """Format amount for display.
    
    BUG: Treats amount as dollars instead of cents!
    Should divide by 100, but doesn't.
    """
    currency = get_currency()
    # BUG: Missing division by 100
    return f"{currency} {amount_in_cents:.2f}"

def save_record(record):
    """Format and save a single record."""
    formatted_amount = format_amount_for_display(record["amount"])
    return {
        "date": record["date"],
        "amount": formatted_amount
    }

def save_all(data):
    """Save all records."""
    return [save_record(r) for r in data]

def run_pipeline(data):
    """Run the complete pipeline."""
    from stage2_validate import validate_all
    from stage3_transform import transform_all
    from stage4_filter import filter_all
    
    validated = validate_all(data)
    transformed = transform_all(validated)
    filtered = filter_all(transformed)
    saved = save_all(filtered)
    return saved