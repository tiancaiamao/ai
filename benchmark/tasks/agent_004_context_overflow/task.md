# Task: Fix the Config Parser (Context Overflow)

## Description
The `config_parser.py` file has a bug in one of its functions.
The file is LARGE (over 3000 lines). You cannot read it all at once.

## Symptoms
When parsing a config file with the `parse_database_config` function,
it returns incorrect connection settings.

## Constraints
- The file is too large to read entirely
- You must use search/grep to find the relevant section
- Do NOT try to read the entire file

## Hint
Search for "database" or "connection" to find the relevant code.