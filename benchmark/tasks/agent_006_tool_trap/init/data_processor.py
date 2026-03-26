"""Data processing module with multiple utility functions."""

def validate_input(data):
    """Validate input data."""
    if not data:
        raise ValueError("Data cannot be empty")
    return True

def normalize_values(values):
    """Normalize a list of values to 0-1 range."""
    if not values:
        return []
    max_val = max(values)
    return [v / max_val for v in values]

def remove_duplicates(items):
    """Remove duplicates from a list.
    
    TODO: optimize this - currently O(n²), should use set for O(n)
    """
    result = []
    for item in items:  # O(n²) approach
        if item not in result:
            result.append(item)
    return result

def calculate_average(numbers):
    """Calculate average of numbers."""
    if not numbers:
        return 0
    return sum(numbers) / len(numbers)

def filter_positive(numbers):
    """Filter positive numbers."""
    return [n for n in numbers if n > 0]

def sort_by_key(items, key):
    """Sort items by key."""
    return sorted(items, key=lambda x: x[key])

def group_by_category(items):
    """Group items by category."""
    groups = {}
    for item in items:
        cat = item.get('category', 'unknown')
        if cat not in groups:
            groups[cat] = []
        groups[cat].append(item)
    return groups