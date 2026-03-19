"""CSV Parser module."""

def parse_csv_line(line):
    """Parse a single CSV line into fields.
    
    BUG: Doesn't handle quoted fields with commas correctly.
    """
    # Simple split by comma - doesn't handle quotes
    return line.split(",")

def parse_csv(content):
    """Parse CSV content into list of rows."""
    lines = content.strip().split("\n")
    return [parse_csv_line(line) for line in lines]

def parse_csv_file(filepath):
    """Parse CSV file."""
    with open(filepath) as f:
        return parse_csv(f.read())