# Task: Implement BASIC Interpreter

## Description

Implement a simplified BASIC interpreter in Go that can execute .bas files.

## Requirements

Read `setup/requirements.md` for the full specification.

### Supported Statements
- PRINT - output to stdout (semicolon suppresses newline)
- INPUT - read from stdin into variable
- LET - variable assignment (LET keyword optional)
- FOR/NEXT - loop (step is always 1)
- IF/THEN - conditional (no ELSE support)
- GOTO - jump to line number
- END - terminate program
- REM - comments

### Expressions
- Arithmetic: `+`, `-`, `*`, `/`
- Comparison: `=`, `<>`, `<`, `>`, `<=`, `>=`
- Unary negation: `-`
- Parentheses for grouping

### Variables
- Numeric variables (e.g., `X`) - default to 0
- String variables (e.g., `N$`) - default to ""
- Case-insensitive names

### CLI
```
basic [-debug] <filename.bas>
```

## Example Files

Test your implementation against the example files in `setup/examples/`:
- `example1.bas` - Simple print
- `example2.bas` - Infinite loop (GOTO)
- `example3.bas` - FOR loop
- `example4.bas` - Counter with GOTO
- `example5.bas` - String input
- `example6.bas` - Numeric input and arithmetic
- `example7.bas` - IF/THEN
- `example8.bas` - Number guessing game

## Success Criteria

1. The program compiles: `go build -o basic`
2. `example1.bas` outputs "hello"
3. `example3.bas` outputs "hello" 10 times
4. `example5.bas` handles string input
5. `example6.bas` handles numeric input and arithmetic
