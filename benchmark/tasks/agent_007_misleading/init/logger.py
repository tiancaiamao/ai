"""Logger module with timestamp bug."""

from datetime import datetime
import time

class Logger:
    def __init__(self):
        self.logs = []
        self._start_time = None  # BUG: Should be time.time() but is None
    
    def _get_timestamp(self):
        """Get current timestamp in seconds from start."""
        if self._start_time is None:
            return 0  # BUG: Returns 0 instead of actual time
        return time.time() - self._start_time
    
    def log(self, message):
        """Log a message with timestamp."""
        timestamp = self._get_timestamp()
        # This will fail if timestamp is not a number
        self.logs.append({
            "timestamp": f"{timestamp:.2f}s",  # Misleading error appears here
            "message": message
        })
    
    def get_logs(self):
        """Get all logs."""
        return self.logs

def create_logger():
    """Create a new logger instance."""
    logger = Logger()
    # BUG: Should call logger._start_time = time.time() here
    return logger