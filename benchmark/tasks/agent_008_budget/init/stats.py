"""Statistics utility functions."""

def calculate_mean(numbers):
    """Calculate mean of numbers."""
    if not numbers:
        return 0
    return sum(numbers) / len(numbers)

def calculate_sum(numbers):
    """Calculate sum of numbers."""
    return sum(numbers)

# TODO: Add calculate_median function