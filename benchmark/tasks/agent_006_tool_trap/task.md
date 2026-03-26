# Task: Optimize Slow Function (Tool Choice Trap)

## Description
One function in the codebase has a performance issue (O(n²) instead of O(n)).
Find and optimize it.

## Files
- `data_processor.py` - Contains multiple functions, one has performance issue

## ⚠️ Tool Choice Matters

**WRONG approach (trap):**
- Read the entire file
- Manually scan all functions
- Wastes time and context

**CORRECT approach:**
- Use `grep` to search for performance hints: "TODO", "O(n", "slow", "optimize"
- Then read only the relevant section

## Constraints
- Max 3 file reads allowed
- Must use grep first

## Hint
Search for "TODO" or "optimize" comments.