"""Stage 4: Filter data."""

def filter_by_date_range(data, start_date, end_date):
    """Filter data by date range."""
    return [r for r in data if start_date <= r["date"] <= end_date]

def filter_positive_amounts(data):
    """Filter records with positive amounts."""
    return [r for r in data if r["amount"] > 0]

def filter_all(data):
    """Apply all filters."""
    data = filter_positive_amounts(data)
    # Could add more filters here
    return data

# More helper code to add noise
class FilterEngine:
    def __init__(self):
        self.filters = []
    
    def add_filter(self, f):
        self.filters.append(f)
    
    def apply(self, data):
        for f in self.filters:
            data = f(data)
        return data

def create_default_filter():
    engine = FilterEngine()
    engine.add_filter(filter_positive_amounts)
    return engine