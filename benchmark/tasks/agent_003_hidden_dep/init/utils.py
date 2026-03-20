"""Utility functions."""

def format_role(role):
    """Format role for display.
    
    BUG: This function uppercases the role, but the role map expects lowercase keys.
    This causes all known roles (admin/user/guest/moderator) to miss.
    """
    return role.upper()


def get_role_label(role):
    """Get human-readable role label."""
    role = format_role(role)  # This breaks matching with role_map keys.
    
    role_map = {
        "admin": "Admin",
        "user": "User",
        "guest": "Guest",
        "moderator": "Moderator"
    }
    return role_map.get(role, "")
