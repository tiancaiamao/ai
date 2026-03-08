package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// Token types
type TokenType int

const (
	TokenNumber TokenType = iota
	TokenString
	TokenIdentifier
	TokenEOF
)

// Token represents a lexical token
type Token struct {
	Type    TokenType
	Value   string
	LineNum int
}

// Line represents a single line of BASIC program
type Line struct {
	Number  int
	Stmts   []Statement
}

// Statement types
type Statement interface{}

// PrintStatement represents PRINT
type PrintStatement struct {
	Exprs     []Expression
	Suppress bool // semicolon suppresses newline
}

// InputStatement represents INPUT
type InputStatement struct {
	Variable string
	IsString bool
}

// LetStatement represents LET
type LetStatement struct {
	Variable string
	Value    Expression
	IsString bool
}

// ForStatement represents FOR
type ForStatement struct {
	Variable string
	Start    Expression
	End      Expression
}

// NextStatement represents NEXT
type NextStatement struct {
	Variable string
}

// IfStatement represents IF/THEN
type IfStatement struct {
	Condition Expression
	ThenStmts []Statement
}

// GotoStatement represents GOTO
type GotoStatement struct {
	LineNum int
}

// EndStatement represents END
type EndStatement struct {
}

// RemStatement represents REM
type RemStatement struct {
	Text string
}

// Assignment represents implicit assignment (LET keyword optional)
type Assignment struct {
	Variable string
	Value    Expression
	IsString bool
}

// Expression types
type Expression interface{}

// NumberExpr represents a numeric literal
type NumberExpr struct {
	Value float64
}

// StringExpr represents a string literal
type StringExpr struct {
	Value string
}

// VariableExpr represents a variable
type VariableExpr struct {
	Name string
}

// BinaryExpr represents a binary operation
type BinaryExpr struct {
	Op    string
	Left  Expression
	Right Expression
}

// UnaryExpr represents a unary operation (negation)
type UnaryExpr struct {
	Op    string
	Right Expression
}

// Lexer
type Lexer struct {
	input  string
	pos    int
	line   int
	tokens []Token
}

func NewLexer(input string) *Lexer {
	return &Lexer{
		input:  input,
		pos:    0,
		line:   1,
		tokens: []Token{},
	}
}

func (l *Lexer) Tokenize() []Token {
	l.tokens = []Token{}
	l.pos = 0
	l.line = 1

	for l.pos < len(l.input) {
		l.skipWhitespace()
		if l.pos >= len(l.input) {
			break
		}

		ch := rune(l.input[l.pos])

		if unicode.IsDigit(ch) {
			l.tokens = append(l.tokens, l.readNumber())
		} else if ch == '"' {
			l.tokens = append(l.tokens, l.readString())
		} else if unicode.IsLetter(ch) || ch == '_' {
			l.tokens = append(l.tokens, l.readIdentifier())
		} else {
			l.tokens = append(l.tokens, Token{
				Type:    TokenIdentifier,
				Value:   string(ch),
				LineNum: l.line,
			})
			l.pos++
		}
	}

	l.tokens = append(l.tokens, Token{Type: TokenEOF, Value: "", LineNum: l.line})
	return l.tokens
}

func (l *Lexer) skipWhitespace() {
	for l.pos < len(l.input) && unicode.IsSpace(rune(l.input[l.pos])) {
		if l.input[l.pos] == '\n' {
			l.line++
		}
		l.pos++
	}
}

func (l *Lexer) readNumber() Token {
	start := l.pos
	hasDot := false
	for l.pos < len(l.input) {
		ch := rune(l.input[l.pos])
		if unicode.IsDigit(ch) {
			l.pos++
		} else if ch == '.' && !hasDot {
			hasDot = true
			l.pos++
		} else {
			break
		}
	}
	return Token{Type: TokenNumber, Value: l.input[start:l.pos], LineNum: l.line}
}

func (l *Lexer) readString() Token {
	l.pos++ // skip opening quote
	start := l.pos
	for l.pos < len(l.input) && l.input[l.pos] != '"' {
		l.pos++
	}
	value := l.input[start:l.pos]
	if l.pos < len(l.input) {
		l.pos++ // skip closing quote
	}
	return Token{Type: TokenString, Value: value, LineNum: l.line}
}

func (l *Lexer) readIdentifier() Token {
	start := l.pos
	for l.pos < len(l.input) {
		ch := rune(l.input[l.pos])
		if !unicode.IsLetter(ch) && !unicode.IsDigit(ch) && ch != '_' && ch != '$' {
			break
		}
		l.pos++
	}
	return Token{Type: TokenIdentifier, Value: l.input[start:l.pos], LineNum: l.line}
}

// Parser
type Parser struct {
	tokens []Token
	pos    int
}

func NewParser(tokens []Token) *Parser {
	return &Parser{
		tokens: tokens,
		pos:    0,
	}
}

func (p *Parser) current() Token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return p.tokens[len(p.tokens)-1]
}

func (p *Parser) peek(offset int) Token {
	if p.pos+offset < len(p.tokens) {
		return p.tokens[p.pos+offset]
	}
	return p.tokens[len(p.tokens)-1]
}

func (p *Parser) advance() Token {
	tok := p.current()
	p.pos++
	return tok
}

func (p *Parser) parseProgram() []Line {
	lines := []Line{}

	for p.current().Type != TokenEOF {
		// Skip empty lines
		if p.current().Type == TokenEOF {
			break
		}

		lineNum := 0

		// Check for line number
		if p.current().Type == TokenNumber {
			num, _ := strconv.Atoi(p.current().Value)
			lineNum = num
			p.advance()
		}

		// Parse statements until end of line
		stmts := []Statement{}
		for p.current().Type != TokenEOF && p.current().Value != "\n" {
			stmt := p.parseStatement()
			if stmt != nil {
				stmts = append(stmts, stmt)
			}

			// Check for colon separator
			if p.current().Type == TokenIdentifier && p.current().Value == ":" {
				p.advance()
			}
		}

		if len(stmts) > 0 {
			lines = append(lines, Line{
				Number:  lineNum,
				Stmts:   stmts,
			})
		}

		// Move past EOF or newline
		if p.current().Type != TokenEOF {
			p.advance()
		}
	}

	return lines
}

func (p *Parser) parseStatement() Statement {
	if p.current().Type == TokenEOF || p.current().Value == "\n" || p.current().Value == ":" {
		return nil
	}

	if p.current().Type != TokenIdentifier {
		p.advance()
		return nil
	}

	keyword := strings.ToUpper(p.current().Value)
	p.advance()

	switch keyword {
	case "PRINT":
		return p.parsePrint()
	case "INPUT":
		return p.parseInput()
	case "LET":
		return p.parseLet()
	case "FOR":
		return p.parseFor()
	case "NEXT":
		return p.parseNext()
	case "IF":
		return p.parseIf()
	case "GOTO":
		return p.parseGoto()
	case "END":
		return &EndStatement{}
	case "REM":
		return p.parseRem()
	default:
		// Could be implicit LET (assignment without LET keyword)
		if p.current().Type == TokenIdentifier || (p.pos > 0 && p.tokens[p.pos-1].Type == TokenIdentifier) {
			// Put the keyword back
			p.pos--
			return p.parseAssignment()
		}
	}

	return nil
}

func (p *Parser) parsePrint() Statement {
	suppress := false
	exprs := []Expression{}

	for p.current().Type != TokenEOF && p.current().Value != "\n" && p.current().Value != ":" {
		// Check for semicolon (suppress newline)
		if p.current().Value == ";" {
			suppress = true
			p.advance()
			break
		}

		// Skip comma for now (could be used as separator)
		if p.current().Value == "," {
			p.advance()
			continue
		}

		expr := p.parseExpression()
		if expr != nil {
			exprs = append(exprs, expr)
		}

		// Check for semicolon after expression
		if p.current().Value == ";" {
			suppress = true
			p.advance()
			break
		}
	}

	return &PrintStatement{
		Exprs:     exprs,
		Suppress:  suppress,
	}
}

func (p *Parser) parseInput() Statement {
	varName := p.current().Value
	p.advance()

	isString := strings.HasSuffix(varName, "$")

	return &InputStatement{
		Variable: strings.ToUpper(varName),
		IsString: isString,
	}
}

func (p *Parser) parseLet() Statement {
	varName := p.current().Value
	p.advance()

	isString := strings.HasSuffix(varName, "$")

	// Skip = sign
	if p.current().Value == "=" {
		p.advance()
	}

	value := p.parseExpression()

	return &LetStatement{
		Variable: strings.ToUpper(varName),
		Value:    value,
		IsString: isString,
	}
}

func (p *Parser) parseAssignment() Statement {
	varName := p.current().Value
	p.advance()

	isString := strings.HasSuffix(varName, "$")

	// Skip = sign
	if p.current().Value == "=" {
		p.advance()
	}

	value := p.parseExpression()

	return &Assignment{
		Variable: strings.ToUpper(varName),
		Value:    value,
		IsString: isString,
	}
}

func (p *Parser) parseFor() Statement {
	varName := p.current().Value
	p.advance()

	// Skip = sign
	if p.current().Value == "=" {
		p.advance()
	}

	start := p.parseExpression()

	// Skip TO
	if p.current().Type == TokenIdentifier && strings.ToUpper(p.current().Value) == "TO" {
		p.advance()
	}

	end := p.parseExpression()

	return &ForStatement{
		Variable: strings.ToUpper(varName),
		Start:     start,
		End:       end,
	}
}

func (p *Parser) parseNext() Statement {
	varName := p.current().Value
	p.advance()

	return &NextStatement{
		Variable: strings.ToUpper(varName),
	}
}

func (p *Parser) parseIf() Statement {
	condition := p.parseExpression()

	// Skip THEN
	if p.current().Type == TokenIdentifier && strings.ToUpper(p.current().Value) == "THEN" {
		p.advance()
	}

	// Parse then statements
	thenStmts := []Statement{}
	for p.current().Type != TokenEOF && p.current().Value != "\n" && p.current().Value != ":" {
		stmt := p.parseStatement()
		if stmt != nil {
			thenStmts = append(thenStmts, stmt)
		}
		if p.current().Value == ":" {
			p.advance()
			break // Only one statement after THEN (or multiple with :)
		}
	}

	return &IfStatement{
		Condition: condition,
		ThenStmts:  thenStmts,
	}
}

func (p *Parser) parseGoto() Statement {
	lineNum, _ := strconv.Atoi(p.current().Value)
	p.advance()

	return &GotoStatement{
		LineNum: lineNum,
	}
}

func (p *Parser) parseRem() Statement {
	text := ""
	// Consume everything until end of line
	for p.current().Type != TokenEOF && p.current().Value != "\n" {
		text += p.current().Value + " "
		p.advance()
	}

	return &RemStatement{
		Text: strings.TrimSpace(text),
	}
}

// Expression parsing - using precedence climbing
func (p *Parser) parseExpression() Expression {
	return p.parseComparison()
}

func (p *Parser) parseComparison() Expression {
	expr := p.parseAddSub()

	for {
		op := p.current().Value
		if op == "=" || op == "<>" || op == "<" || op == ">" || op == "<=" || op == ">=" {
			p.advance()
			right := p.parseAddSub()
			expr = &BinaryExpr{Op: op, Left: expr, Right: right}
		} else {
			break
		}
	}

	return expr
}

func (p *Parser) parseAddSub() Expression {
	expr := p.parseMulDiv()

	for {
		op := p.current().Value
		if op == "+" || op == "-" {
			p.advance()
			right := p.parseMulDiv()
			expr = &BinaryExpr{Op: op, Left: expr, Right: right}
		} else {
			break
		}
	}

	return expr
}

func (p *Parser) parseMulDiv() Expression {
	expr := p.parseUnary()

	for {
		op := p.current().Value
		if op == "*" || op == "/" {
			p.advance()
			right := p.parseUnary()
			expr = &BinaryExpr{Op: op, Left: expr, Right: right}
		} else {
			break
		}
	}

	return expr
}

func (p *Parser) parseUnary() Expression {
	if p.current().Value == "-" {
		p.advance()
		right := p.parseUnary()
		return &UnaryExpr{Op: "-", Right: right}
	}

	return p.parsePrimary()
}

func (p *Parser) parsePrimary() Expression {
	tok := p.current()

	if tok.Type == TokenNumber {
		p.advance()
		val, _ := strconv.ParseFloat(tok.Value, 64)
		return &NumberExpr{Value: val}
	}

	if tok.Type == TokenString {
		p.advance()
		return &StringExpr{Value: tok.Value}
	}

	if tok.Type == TokenIdentifier {
		name := strings.ToUpper(tok.Value)
		p.advance()
		return &VariableExpr{Name: name}
	}

	if tok.Value == "(" {
		p.advance()
		expr := p.parseExpression()
		if p.current().Value == ")" {
			p.advance()
		}
		return expr
	}

	p.advance()
	return &NumberExpr{Value: 0}
}

// Interpreter
type Interpreter struct {
	program   []Line
	lineIndex int
	variables map[string]float64
	stringVars map[string]string
	forLoops   map[string]*ForLoopState
	lineMap    map[int]int // line number to index
	debug      bool
}

type ForLoopState struct {
	Start     float64
	End       float64
	LineIndex int
}

func NewInterpreter(program []Line, debug bool) *Interpreter {
	lineMap := make(map[int]int)
	for i, line := range program {
		lineMap[line.Number] = i
	}

	return &Interpreter{
		program:    program,
		lineIndex:  0,
		variables:  make(map[string]float64),
		stringVars: make(map[string]string),
		forLoops:   make(map[string]*ForLoopState),
		lineMap:    lineMap,
		debug:      debug,
	}
}

func (i *Interpreter) Run() {
	for i.lineIndex < len(i.program) {
		line := i.program[i.lineIndex]

		for _, stmt := range line.Stmts {
			i.executeStatement(stmt)
			if i.lineIndex >= len(i.program) {
				break
			}
		}
	}
}

func (i *Interpreter) executeStatement(stmt Statement) {
	switch s := stmt.(type) {
	case *PrintStatement:
		i.executePrint(s)
	case *InputStatement:
		i.executeInput(s)
	case *LetStatement:
		i.executeLet(s)
	case *Assignment:
		i.executeAssignment(s)
	case *ForStatement:
		i.executeFor(s)
	case *NextStatement:
		i.executeNext(s)
	case *IfStatement:
		i.executeIf(s)
	case *GotoStatement:
		i.executeGoto(s)
	case *EndStatement:
		i.lineIndex = len(i.program)
	case *RemStatement:
		// Do nothing
	}
}

func (i *Interpreter) executePrint(s *PrintStatement) {
	output := ""
	for j, expr := range s.Exprs {
		val := i.evalExpression(expr)
		if j > 0 {
			output += " "
		}
		output += fmt.Sprintf("%v", val)
	}

	if !s.Suppress {
		output += "\n"
	}

	fmt.Print(output)
}

func (i *Interpreter) executeInput(s *InputStatement) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("? ")
	text, _ := reader.ReadString('\n')
	text = strings.TrimSpace(text)

	if s.IsString {
		i.stringVars[s.Variable] = text
	} else {
		val, err := strconv.ParseFloat(text, 64)
		if err != nil {
			i.variables[s.Variable] = 0
		} else {
			i.variables[s.Variable] = val
		}
	}
}

func (i *Interpreter) executeLet(s *LetStatement) {
	val := i.evalExpression(s.Value)
	i.variables[s.Variable] = val
}

func (i *Interpreter) executeAssignment(s *Assignment) {
	val := i.evalExpression(s.Value)
	i.variables[s.Variable] = val
}

func (i *Interpreter) executeFor(s *ForStatement) {
	start := i.evalExpression(s.Start)
	end := i.evalExpression(s.End)

	i.variables[s.Variable] = start

	i.forLoops[s.Variable] = &ForLoopState{
		Start:     start,
		End:       end,
		LineIndex: i.lineIndex,
	}
}

func (i *Interpreter) executeNext(s *NextStatement) {
	loop, ok := i.forLoops[s.Variable]
	if !ok {
		return
	}

	// Increment the loop variable
	i.variables[s.Variable]++

	// Check if we've reached the end
	if i.variables[s.Variable] <= loop.End {
		// Jump back to the FOR statement
		i.lineIndex = loop.LineIndex
	} else {
		// Remove the loop state
		delete(i.forLoops, s.Variable)
	}
}

func (i *Interpreter) executeIf(s *IfStatement) {
	cond := i.evalExpression(s.Condition)
	if cond != 0 {
		for _, stmt := range s.ThenStmts {
			i.executeStatement(stmt)
		}
	}
}

func (i *Interpreter) executeGoto(s *GotoStatement) {
	newIndex, ok := i.lineMap[s.LineNum]
	if ok {
		i.lineIndex = newIndex
	}
}

func (i *Interpreter) evalExpression(expr Expression) float64 {
	switch e := expr.(type) {
	case *NumberExpr:
		return e.Value
	case *StringExpr:
		// String in numeric context - return 0
		return 0
	case *VariableExpr:
		if strings.HasSuffix(e.Name, "$") {
			return 0 // String variable in numeric context
		}
		return i.variables[e.Name]
	case *BinaryExpr:
		return i.evalBinaryExpr(e)
	case *UnaryExpr:
		return -i.evalExpression(e.Right)
	}
	return 0
}

func (i *Interpreter) evalBinaryExpr(expr *BinaryExpr) float64 {
	left := i.evalExpression(expr.Left)
	right := i.evalExpression(expr.Right)

	switch expr.Op {
	case "+":
		return left + right
	case "-":
		return left - right
	case "*":
		return left * right
	case "/":
		if right == 0 {
			return 0
		}
		return left / right
	case "=":
		if left == right {
			return 1
		}
		return 0
	case "<>":
		if left != right {
			return 1
		}
		return 0
	case "<":
		if left < right {
			return 1
		}
		return 0
	case ">":
		if left > right {
			return 1
		}
		return 0
	case "<=":
		if left <= right {
			return 1
		}
		return 0
	case ">=":
		if left >= right {
			return 1
		}
		return 0
	}
	return 0
}

func (i *Interpreter) printVariables() {
	fmt.Println("\nVariables:")
	for k, v := range i.variables {
		fmt.Printf("  %s = %v\n", k, v)
	}
	for k, v := range i.stringVars {
		fmt.Printf("  %s = \"%s\"\n", k, v)
	}
}

func main() {
	debug := false
	filename := ""

	for _, arg := range os.Args[1:] {
		if arg == "-debug" {
			debug = true
		} else {
			filename = arg
		}
	}

	if filename == "" {
		fmt.Println("Usage: basic [-debug] <filename.bas>")
		os.Exit(1)
	}

	// Read the file
	content, err := os.ReadFile(filename)
	if err != nil {
		fmt.Printf("Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Tokenize
	lexer := NewLexer(string(content))
	tokens := lexer.Tokenize()

	// Parse
	parser := NewParser(tokens)
	program := parser.parseProgram()

	// Interpret
	interpreter := NewInterpreter(program, debug)
	interpreter.Run()

	if debug {
		interpreter.printVariables()
	}
}