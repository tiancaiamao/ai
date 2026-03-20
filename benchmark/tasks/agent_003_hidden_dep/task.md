# Task: Debug the User API (Hidden Dependency)

## Description
The user API is returning incorrect results. The `get_user_display_name` function
should return "John Doe (Admin)" for admin users, but it's returning "John Doe ()".

## Symptoms
```python
user = User(name="John", role="admin")
get_user_display_name(user)  # Returns "John ()" instead of "John (Admin)"
```

## Files
- `user.py` - User model
- `api.py` - API functions
- `utils.py` - Utility functions

## Hint
The bug might not be where you think it is. Follow the function calls.