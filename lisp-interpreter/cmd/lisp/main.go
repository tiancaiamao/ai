package main

import (
	"bufio"
	"fmt"
	"lisp-interpreter/lisp"
	"os"
	"strings"
)

func main() {
	// Initialize the evaluator with built-ins and special forms
	lisp.InitEvaluator()

	// Create global environment (inherits from the global env where built-ins are registered)
	env := lisp.NewEnclosedEnvironment(lisp.GlobalEnv())

	// Start REPL
	fmt.Println("Lisp Interpreter")
	fmt.Println("Type 'exit' or 'quit' to exit")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	reader := bufio.NewReader(os.Stdin)

	for {
		// Print prompt
		fmt.Print("> ")

		// Read input (handle multi-line)
		var input string
		var err error

		input, err = readMultiLine(reader)
		if err != nil {
			if err.Error() == "EOF" {
				fmt.Println("\nGoodbye!")
				break
			}
			fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
			continue
		}

		// Trim whitespace
		input = strings.TrimSpace(input)

		// Skip empty input
		if input == "" {
			continue
		}

		// Handle exit commands
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}

		// Lexical analysis
		lexer := lisp.NewLexer(input)
		tokens, err := lexer.Lex()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Lexer error: %v\n", err)
			continue
		}

		// Parse
		parser := lisp.NewParser(tokens)
		exprs, err := parser.Parse()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Parse error: %v\n", err)
			continue
		}

		// Evaluate each expression
		for _, expr := range exprs {
			result, err := lisp.Eval(expr, env)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Runtime error: %v\n", err)
				continue
			}

			// Print result (don't print nil)
			if result != nil && !isNil(result) {
				fmt.Println(result.String())
			}
		}

		_ = scanner // scanner is not used, but kept for potential future use
	}
}

// readMultiLine reads input, supporting multi-line expressions by tracking parentheses.
func readMultiLine(reader *bufio.Reader) (string, error) {
	var lines []string
	parenDepth := 0
	inString := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if len(lines) == 0 {
				return "", err
			}
			break
		}

		// Track parentheses depth and string state
		for i, ch := range line {
			if ch == '"' && (i == 0 || line[i-1] != '\\') {
				inString = !inString
			}
			if !inString {
				if ch == '(' {
					parenDepth++
				} else if ch == ')' {
					parenDepth--
				}
			}
		}

		lines = append(lines, strings.TrimSuffix(line, "\n"))

		// If we've balanced parentheses, we're done
		if parenDepth <= 0 && !inString {
			break
		}

		// Continue with next line, show continuation prompt
		fmt.Print("  ")
	}

	return strings.Join(lines, "\n"), nil
}

// isNil checks if the expression is the nil value.
func isNil(expr lisp.Expr) bool {
	// Check if it's a Nil pointer type
	if n, ok := expr.(*lisp.Nil); ok {
		return n != nil
	}
	return false
}