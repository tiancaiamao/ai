def custom_sort(arr):
    """Sort array in ascending order using custom implementation.
    
    WARNING: This function has a bug!
    It should sort ascending but sorts descending.
    """
    n = len(arr)
    for i in range(n):
        for j in range(0, n - i - 1):
            # BUG: Using < instead of > causes descending order
            if arr[j] < arr[j + 1]:  # Should be > for ascending
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
    return arr

def reverse_array(arr):
    """Reverse the array."""
    return arr[::-1]