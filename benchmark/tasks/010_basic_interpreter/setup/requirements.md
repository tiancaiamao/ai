A simplified old basic interpreter for .bas files.

# Target

* Language: Golang
* Usage: `basic [-debug] <filename.bas>`

# Supported Statements

* PRINT - output to stdout (semicolon suppresses newline)
* INPUT - read from stdin into variable
* LET - variable assignment (LET keyword optional)
* FOR/NEXT - loop (step is always 1)
* IF/THEN - conditional (no ELSE support)
* GOTO - jump to line number
* END - terminate program
* REM - comments

# Expressions

* Arithmetic: `+`, `-`, `*`, `/`
* Comparison: `=`, `<>`, `<`, `>`, `<=`, `>=`
* Unary negation: `-`
* Parentheses for grouping

# Variables

* Numeric variables (e.g., `X`) - default to 0
* String variables (e.g., `N$`) - default to ""
* Case-insensitive names

# Program Structure

* Each line starts with a line number
* Multiple statements per line separated by `:`
* Lines execute in numeric order

# CLI Options

* `-debug` - print all variables and values after execution

# Examples

* Example files in examples directory: example1.bas - example8.bas
