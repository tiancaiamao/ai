"""Module 01 - Data validation."""


def validate_email(email: str) -> bool:
    """Validate email format."""
    if not email or "@" not in email:
        return False
    parts = email.split("@")
    return len(parts) == 2 and len(parts[0]) > 0 and len(parts[1]) > 0


def validate_phone(phone: str) -> bool:
    """Validate phone number."""
    return phone.isdigit() and 10 <= len(phone) <= 15


def validate_data(data: dict) -> bool:
    """Validate data dictionary."""
    required = ["name", "email", "phone"]
    return all(key in data for key in required)