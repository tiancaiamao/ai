# MOS6502 Assembler in Go

## Overview

A MOS6502 assembler written in Golang that parses assembly source files and outputs machine code in JSON format.
* Implements a CLI which outputs the JSON format.
* Implements a separate Go package with the assembler code.

## API

The assembler is implemented as a separate Go package `asm` with a public function:

```go
func Assemble(source string) (*AssemblerOutput, error)
```

### AssemblerOutput

```go
type AssemblerOutput struct {
    Symbols      map[string]Symbol `json:"symbols"`
    Instructions []Instruction     `json:"instructions"`
}

type Symbol struct {
    Value int    `json:"value"`
    Type  string `json:"type"`
}

type Instruction struct {
    Address string `json:"address"`
    Disasm  string `json:"disasm"`
    Bytes   []int  `json:"bytes"`
}
```

#### Symbol Fields

| Field   | Description                                                                  |
|---------|------------------------------------------------------------------------------|
| `value` | Numeric value of the symbol                                                  |
| `type`  | Symbol type: `"constant"` (defined with `=`) or `"label"` (defined with `:`) |

#### Instruction Fields

| Field     | Description                                      |
|-----------|--------------------------------------------------|
| `address` | Memory address in hex format (e.g., "0x8000")    |
| `disasm`  | Human-readable disassembly (mnemonic + operand)  |
| `bytes`   | Machine code bytes (opcode followed by operands) |

#### Example Output

```json
{
  "symbols": {
    "SCREEN": {
      "value": 1024,
      "type": "constant"
    },
    "ptr": {
      "value": 251,
      "type": "constant"
    },
    "start": {
      "value": 32768,
      "type": "label"
    },
    "loop": {
      "value": 32770,
      "type": "label"
    }
  },
  "instructions": [
    {
      "address": "0x8000",
      "disasm": "LDA #66",
      "bytes": [169, 66]
    },
    {
      "address": "0x8002",
      "disasm": "STA $0",
      "bytes": [133, 0]
    }
  ]
}
```

### AssemblerError

The assembler uses a custom error type that includes the line number:

```go
type AssemblerError struct {
    Line    int
    Message string
}

func (e *AssemblerError) Error() string
```

## Usage

The assembler comes with a CLI which outputs the JSON.

```
asm [-debug] <filename.asm>
```

- `-debug`: Enable debug output
- `<filename.asm>`: Input assembly file

## Input Syntax

### Directives & Definitions

| Element  | Syntax        | Example          |
|----------|---------------|------------------|
| Origin   | `* = $XXXX`   | `* = $8000`      |
| Label    | `name:`       | `start:`         |
| Constant | `NAME = expr` | `SCREEN = $0400` |
| Comment  | `; text`      | `; comment`      |

### Numeric Formats

| Format      | Syntax           | Example        |
|-------------|------------------|----------------|
| Hexadecimal | `$XX` or `$XXXX` | `$42`, `$8000` |
| Decimal     | `nn`             | `40`, `256`    |
| Binary      | `%xxxxxxxx`      | `%10101010`    |

### Operand Modifiers

| Modifier | Meaning     | Example        |
|----------|-------------|----------------|
| `<expr`  | Low byte    | `lda #<SCREEN` |
| `>expr`  | High byte   | `lda #>SCREEN` |
| `expr+n` | Addition    | `sta ptr+1`    |
| `expr-n` | Subtraction | `sta ptr-1`    |

## Addressing Modes

| Mode             | Syntax    | Size | Example       |
|------------------|-----------|------|---------------|
| Implied          | (none)    | 1    | `rts`         |
| Accumulator      | `A`       | 1    | `asl a`       |
| Immediate        | `#$XX`    | 2    | `lda #$42`    |
| Zero Page        | `$XX`     | 2    | `sta $00`     |
| Zero Page,X      | `$XX,X`   | 2    | `lda $10,x`   |
| Zero Page,Y      | `$XX,Y`   | 2    | `ldx $10,y`   |
| Absolute         | `$XXXX`   | 3    | `jmp $8000`   |
| Absolute,X       | `$XXXX,X` | 3    | `lda $1000,x` |
| Absolute,Y       | `$XXXX,Y` | 3    | `lda $1000,y` |
| Indirect         | `($XXXX)` | 3    | `jmp ($FFFC)` |
| Indexed Indirect | `($XX,X)` | 2    | `lda ($40,x)` |
| Indirect Indexed | `($XX),Y` | 2    | `sta (ptr),y` |
| Relative         | `label`   | 2    | `bne loop`    |

## Dependencies

- `opcodes.go` - Opcode definitions, addressing modes, instruction metadata, find opcodes

## Style

- Separate lexer, passes, expr evaluation, symbol handling, error type, into separate Go files. 

## Testing

Test the implementation against all files in the "examples" and "examples_invalid" directory.
