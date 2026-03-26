package asm

import (
	"fmt"
	"strings"
)

// AssemblerError represents an error during assembly
type AssemblerError struct {
	Line    int
	Message string
}

func (e *AssemblerError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Message)
}

// AssemblerOutput is the output of the assembler
type AssemblerOutput struct {
	Symbols      map[string]Symbol    `json:"symbols"`
	Instructions []Instruction        `json:"instructions"`
}

// Symbol represents a symbol in the assembly
type Symbol struct {
	Value int    `json:"value"`
	Type  string `json:"type"` // "constant" or "label"
}

// Instruction represents a single instruction
type Instruction struct {
	Address string `json:"address"`
	Disasm  string `json:"disasm"`
	Bytes   []int  `json:"bytes"`
}

// Assembler holds the state for assembling
type Assembler struct {
	lexer           *Lexer
	pc              int          // Program counter
	origin          int          // Origin address
	symbols         map[string]Symbol
	labels          map[string]int
	constants       map[string]int
	instructions    []Instruction
	currentLine     int
	pendingLabels   []string
}

// NewAssembler creates a new assembler
func NewAssembler(source string) *Assembler {
	return &Assembler{
		lexer:     NewLexer(source),
		pc:        0x8000, // Default origin
		origin:    0x8000,
		symbols:   make(map[string]Symbol),
		labels:    make(map[string]int),
		constants: make(map[string]int),
	}
}

// Assemble assembles the source code and returns the output
func Assemble(source string) (*AssemblerOutput, error) {
	a := NewAssembler(source)
	
	// First pass: collect labels and constants
	if err := a.firstPass(); err != nil {
		return nil, err
	}
	
	// Second pass: generate code
	if err := a.secondPass(); err != nil {
		return nil, err
	}
	
	return a.output(), nil
}

// firstPass collects labels and constants
func (a *Assembler) firstPass() error {
	a.pc = a.origin
	a.lexer = NewLexer(a.lexer.source)
	
	for {
		tok := a.lexer.Peek()
		if tok.Type == TokenEOF {
			break
		}
		
		a.currentLine = tok.Line
		
		// Skip empty lines and comments
		if tok.Type == TokenEOF {
			break
		}
		
		// Handle origin directive
		if tok.Type == TokenSymbol && tok.Value == "*" {
			a.lexer.Next()
			eqTok := a.lexer.Next()
			if eqTok.Type != TokenSymbol || eqTok.Value != "=" {
				return &AssemblerError{Line: a.currentLine, Message: "expected = after *"}
			}
			originVal, err := a.parseExpression()
			if err != nil {
				return &AssemblerError{Line: a.currentLine, Message: err.Error()}
			}
			a.origin = originVal
			a.pc = originVal
			continue
		}
		
		// Handle label definition (identifier followed by :)
		if tok.Type == TokenIdentifier {
			// Look ahead to see if it's a label
			labelName := tok.Value
			peeked := a.lexer.Peek()
			if peeked.Type == TokenSymbol && peeked.Value == ":" {
				// It's a label
				a.lexer.Next() // consume label name
				a.lexer.Next() // consume :
				a.labels[labelName] = a.pc
				a.symbols[labelName] = Symbol{Value: a.pc, Type: "label"}
				continue
			}

			// Check for constant definition: IDENT = expr
			peeked = a.lexer.Peek()
			if peeked.Type == TokenSymbol && peeked.Value == "=" {
				a.lexer.Next() // consume identifier
				a.lexer.Next() // consume =
				constVal, err := a.parseExpression()
				if err != nil {
					return &AssemblerError{Line: a.currentLine, Message: err.Error()}
				}
				a.constants[labelName] = constVal
				a.symbols[labelName] = Symbol{Value: constVal, Type: "constant"}
				continue
			}
		}
		
		// Handle mnemonic (instruction)
		if tok.Type == TokenMnemonic {
			mnemonic := tok.Value
			a.lexer.Next()
			
			// Find the opcode and determine size
			mode, _, err := a.parseOperand()
			if err != nil {
				return &AssemblerError{Line: a.currentLine, Message: err.Error()}
			}
			
			opcode, found := FindOpcode(mnemonic, mode)
			if !found {
				return &AssemblerError{Line: a.currentLine, Message: fmt.Sprintf("unknown instruction %s with mode %v", mnemonic, mode)}
			}
			
			a.pc += opcode.Size
			continue
		}
		
		// Skip unknown tokens
		a.lexer.Next()
	}
	
	return nil
}

// secondPass generates the actual machine code
func (a *Assembler) secondPass() error {
	a.pc = a.origin
	a.lexer = NewLexer(a.lexer.source)
	
	for {
		tok := a.lexer.Peek()
		if tok.Type == TokenEOF {
			break
		}
		
		a.currentLine = tok.Line
		
		// Handle origin directive
		if tok.Type == TokenSymbol && tok.Value == "*" {
			a.lexer.Next()
			eqTok := a.lexer.Next()
			if eqTok.Type != TokenSymbol || eqTok.Value != "=" {
				return &AssemblerError{Line: a.currentLine, Message: "expected = after *"}
			}
			originVal, err := a.parseExpression()
			if err != nil {
				return &AssemblerError{Line: a.currentLine, Message: err.Error()}
			}
			a.origin = originVal
			a.pc = originVal
			continue
		}
		
		// Handle label definition
		if tok.Type == TokenIdentifier {
			peeked := a.lexer.Peek()
			if peeked.Type == TokenSymbol && peeked.Value == ":" {
				a.lexer.Next()
				a.lexer.Next()
				continue
			}
			
			// Check for constant definition
			peeked = a.lexer.Peek()
			if peeked.Type == TokenSymbol && peeked.Value == "=" {
				a.lexer.Next()
				a.lexer.Next()
				_, err := a.parseExpression()
				if err != nil {
					return &AssemblerError{Line: a.currentLine, Message: err.Error()}
				}
				continue
			}
		}
		
		// Handle mnemonic
		if tok.Type == TokenMnemonic {
			mnemonic := tok.Value
			address := a.pc
			a.lexer.Next()
			
			mode, operand, err := a.parseOperand()
			if err != nil {
				return &AssemblerError{Line: a.currentLine, Message: err.Error()}
			}
			
			opcode, found := FindOpcode(mnemonic, mode)
			if !found {
				return &AssemblerError{Line: a.currentLine, Message: fmt.Sprintf("unknown instruction %s with mode %v", mnemonic, mode)}
			}
			
			// Generate the instruction bytes
			bytes := []int{int(opcode.Opcode)}
			
			// Handle relative addressing for branches
			if mode == AddrRelative {
				target, err := a.resolveOperand(operand)
				if err != nil {
					return &AssemblerError{Line: a.currentLine, Message: err.Error()}
				}
				offset := target - (a.pc + 2)
				if offset < -128 || offset > 127 {
					return &AssemblerError{Line: a.currentLine, Message: fmt.Sprintf("branch out of range: %d", offset)}
				}
				bytes = append(bytes, offset&0xFF)
			} else {
				// Add operand bytes
				for _, b := range operand {
					bytes = append(bytes, int(b))
				}
			}
			
			// Format the disassembly
			disasm := opcode.FormatOperand(operand)
			if disasm == "" {
				disasm = mnemonic
			} else {
				disasm = mnemonic + disasm
			}
			
			inst := Instruction{
				Address: fmt.Sprintf("0x%04X", address),
				Disasm:  disasm,
				Bytes:   bytes,
			}
			a.instructions = append(a.instructions, inst)
			
			a.pc += opcode.Size
			continue
		}
		
		// Skip unknown tokens
		a.lexer.Next()
	}
	
	return nil
}

// parseOperand parses the operand and returns addressing mode and operand bytes
func (a *Assembler) parseOperand() (AddrMode, []byte, error) {
	tok := a.lexer.Peek()
	
	// Implied / Accumulator
	if tok.Type == TokenEOF || tok.Type == TokenSymbol {
		// Check for accumulator
		if tok.Type == TokenIdentifier && strings.ToUpper(tok.Value) == "A" {
			a.lexer.Next()
			return AddrAccumulator, nil, nil
		}
		// Implied
		if tok.Type == TokenSymbol {
			// Could be end of line
			return AddrImplied, nil, nil
		}
	}
	
	// Immediate: #$
	if tok.Type == TokenSymbol && tok.Value == "#" {
		a.lexer.Next()
		val, err := a.parseExpression()
		if err != nil {
			return AddrImmediate, nil, err
		}
		return AddrImmediate, []byte{byte(val & 0xFF)}, nil
	}
	
	// Indirect: ($XXXX)
	if tok.Type == TokenSymbol && tok.Value == "(" {
		a.lexer.Next()
		tok := a.lexer.Peek()
		
		// Check for indexed indirect ($XX,X) or ($XX),Y
		if tok.Type == TokenNumber {
			addr, err := a.parseExpression()
			if err != nil {
				return AddrIndirect, nil, err
			}
			
			tok := a.lexer.Peek()
			if tok.Type == TokenSymbol && tok.Value == "," {
				a.lexer.Next()
				indexTok := a.lexer.Next()
				if strings.ToUpper(indexTok.Value) == "X" {
					// ($XX,X) - Indexed Indirect
					closeTok := a.lexer.Next()
					if closeTok.Value != ")" {
						return AddrIndexedIndirect, nil, fmt.Errorf("expected )")
					}
					return AddrIndexedIndirect, []byte{byte(addr & 0xFF)}, nil
				}
			}
			
			// Check for )),Y
			closeTok := a.lexer.Next()
			if closeTok.Value != ")" {
				return AddrIndirect, nil, fmt.Errorf("expected )")
			}
			
			tok = a.lexer.Peek()
			if tok.Type == TokenSymbol && tok.Value == "," {
				a.lexer.Next()
				indexTok := a.lexer.Next()
				if strings.ToUpper(indexTok.Value) == "Y" {
					// ($XX),Y - Indirect Indexed
					return AddrIndirectIndexed, []byte{byte(addr & 0xFF)}, nil
				}
			}
			
			// Just ($XXXX) - Indirect
			return AddrIndirect, []byte{byte(addr & 0xFF), byte((addr >> 8) & 0xFF)}, nil
		}
		
		return AddrIndirect, nil, fmt.Errorf("invalid indirect operand")
	}
	
	// Zero Page or Absolute: $XX or $XXXX
	if tok.Type == TokenNumber {
		addr, err := a.parseExpression()
		if err != nil {
			return AddrZeroPage, nil, err
		}
		
		tok := a.lexer.Peek()
		
		// Check for ,X or ,Y
		if tok.Type == TokenSymbol && tok.Value == "," {
			a.lexer.Next()
			indexTok := a.lexer.Next()
			upper := strings.ToUpper(indexTok.Value)
			
			if upper == "X" {
				// $XX,X or $XXXX,X
				if addr <= 0xFF {
					return AddrZeroPageX, []byte{byte(addr & 0xFF)}, nil
				}
				return AddrAbsoluteX, []byte{byte(addr & 0xFF), byte((addr >> 8) & 0xFF)}, nil
			}
			if upper == "Y" {
				// $XX,Y or $XXXX,Y
				if addr <= 0xFF {
					return AddrZeroPageY, []byte{byte(addr & 0xFF)}, nil
				}
				return AddrAbsoluteY, []byte{byte(addr & 0xFF), byte((addr >> 8) & 0xFF)}, nil
			}
		}
		
		// Plain address
		if addr <= 0xFF {
			return AddrZeroPage, []byte{byte(addr & 0xFF)}, nil
		}
		return AddrAbsolute, []byte{byte(addr & 0xFF), byte((addr >> 8) & 0xFF)}, nil
	}
	
	// Identifier (could be label for relative branch)
	if tok.Type == TokenIdentifier {
		// Just consume the identifier and treat as relative
		a.lexer.Next()
		return AddrRelative, []byte(tok.Value), nil
	}
	
	// Default to implied
	return AddrImplied, nil, nil
}

// resolveOperand resolves an operand to its numeric value
func (a *Assembler) resolveOperand(operand []byte) (int, error) {
	if len(operand) == 0 {
		return 0, nil
	}
	
	// Check if it's a label name
	if len(operand) > 0 && string(operand[0]) == "$" {
		// It's a hex number
		addr := 0
		_, err := fmt.Sscanf(string(operand), "$%x", &addr)
		if err != nil {
			return 0, err
		}
		return addr, nil
	}
	
	// Try as identifier
	name := string(operand)
	
	// Check labels first
	if val, ok := a.labels[name]; ok {
		return val, nil
	}
	
	// Check constants
	if val, ok := a.constants[name]; ok {
		return val, nil
	}
	
	// Check symbols
	if sym, ok := a.symbols[name]; ok {
		return sym.Value, nil
	}
	
	return 0, fmt.Errorf("undefined symbol: %s", name)
}

// parseExpression parses an expression and returns its value
func (a *Assembler) parseExpression() (int, error) {
	return a.parseAddSub()
}

func (a *Assembler) parseAddSub() (int, error) {
	left, err := a.parseMulDiv()
	if err != nil {
		return 0, err
	}
	
	for {
		tok := a.lexer.Peek()
		if tok.Type != TokenSymbol || (tok.Value != "+" && tok.Value != "-") {
			break
		}
		a.lexer.Next()
		right, err := a.parseMulDiv()
		if err != nil {
			return 0, err
		}
		if tok.Value == "+" {
			left = left + right
		} else {
			left = left - right
		}
	}
	
	return left, nil
}

func (a *Assembler) parseMulDiv() (int, error) {
	left, err := a.parseUnary()
	if err != nil {
		return 0, err
	}
	
	for {
		tok := a.lexer.Peek()
		if tok.Type != TokenSymbol || (tok.Value != "*" && tok.Value != "/") {
			break
		}
		a.lexer.Next()
		right, err := a.parseUnary()
		if err != nil {
			return 0, err
		}
		if tok.Value == "*" {
			left = left * right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left = left / right
		}
	}
	
	return left, nil
}

func (a *Assembler) parseUnary() (int, error) {
	tok := a.lexer.Peek()
	
	// Handle < for low byte
	if tok.Type == TokenSymbol && tok.Value == "<" {
		a.lexer.Next()
		val, err := a.parsePrimary()
		if err != nil {
			return 0, err
		}
		return val & 0xFF, nil
	}
	
	// Handle > for high byte
	if tok.Type == TokenSymbol && tok.Value == ">" {
		a.lexer.Next()
		val, err := a.parsePrimary()
		if err != nil {
			return 0, err
		}
		return (val >> 8) & 0xFF, nil
	}
	
	// Handle unary minus
	if tok.Type == TokenSymbol && tok.Value == "-" {
		a.lexer.Next()
		val, err := a.parsePrimary()
		if err != nil {
			return 0, err
		}
		return -val, nil
	}
	
	return a.parsePrimary()
}

func (a *Assembler) parsePrimary() (int, error) {
	tok := a.lexer.Peek()
	
	// Number
	if tok.Type == TokenNumber {
		a.lexer.Next()
		return a.parseNumber(tok.Value)
	}
	
	// Identifier
	if tok.Type == TokenIdentifier {
		a.lexer.Next()
		name := tok.Value
		
		// Check constants
		if val, ok := a.constants[name]; ok {
			return val, nil
		}
		
		// Check labels (for forward references, we'll handle this in second pass)
		if val, ok := a.labels[name]; ok {
			return val, nil
		}
		
		// Check symbols
		if sym, ok := a.symbols[name]; ok {
			return sym.Value, nil
		}
		
		// For first pass, we might not have the value yet
		// Return 0 and let second pass handle it
		return 0, nil
	}
	
	// Parentheses
	if tok.Type == TokenSymbol && tok.Value == "(" {
		a.lexer.Next()
		val, err := a.parseExpression()
		if err != nil {
			return 0, err
		}
		closeTok := a.lexer.Next()
		if closeTok.Value != ")" {
			return 0, fmt.Errorf("expected )")
		}
		return val, nil
	}
	
	// Check for label reference (for relative addressing)
	if tok.Type == TokenIdentifier {
		a.lexer.Next()
		return 0, nil
	}
	
	return 0, fmt.Errorf("unexpected token: %v", tok)
}

func (a *Assembler) parseNumber(value string) (int, error) {
	if len(value) == 0 {
		return 0, fmt.Errorf("empty number")
	}
	
	if value[0] == '$' {
		// Hex
		var n int
		_, err := fmt.Sscanf(value[1:], "%x", &n)
		if err != nil {
			return 0, fmt.Errorf("invalid hex number: %s", value)
		}
		return n, nil
	}
	
	if value[0] == '%' {
		// Binary
		var n int
		_, err := fmt.Sscanf(value[1:], "%b", &n)
		if err != nil {
			return 0, fmt.Errorf("invalid binary number: %s", value)
		}
		return n, nil
	}
	
	// Decimal
	var n int
	_, err := fmt.Sscanf(value, "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", value)
	}
	return n, nil
}

// output returns the assembler output
func (a *Assembler) output() *AssemblerOutput {
	return &AssemblerOutput{
		Symbols:      a.symbols,
		Instructions: a.instructions,
	}
}