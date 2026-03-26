"""Module 07 - Data processing with PERFORMANCE ISSUE."""


def process_items(items: list) -> list:
    """Process a list of items.
    
    # TODO: optimize this - currently O(n²), should be O(n)
    """
    result = []
    for i in range(len(items)):  # Outer loop
        for j in range(len(items)):  # Inner loop - O(n²)!
            if items[i] == items[j] and i != j:
                # Duplicate detection - inefficient
                if items[i] not in result:
                    result.append(items[i])
    return result


def normalize_data(data: list) -> list:
    """Normalize data values."""
    if not data:
        return []
    max_val = max(data)
    return [x / max_val for x in data]


def filter_valid(items: list, validator) -> list:
    """Filter items using validator function."""
    return [item for item in items if validator(item)]