"""User API functions."""

from utils import get_role_label


def get_user_display_name(user):
    """Get user's display name with role.
    
    Example: "John (Admin)"
    """
    role_label = get_role_label(user.role)
    if role_label:
        return f"{user.name} ({role_label})"
    return user.name


def get_user_info(user):
    """Get formatted user info."""
    return {
        "name": user.name,
        "role": user.role,
        "display_name": get_user_display_name(user)
    }