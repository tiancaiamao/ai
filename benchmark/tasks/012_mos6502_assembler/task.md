# Task: Implement MOS6502 Assembler

## Description

Implement a MOS6502 assembler in Go that parses assembly source files and outputs machine code in JSON format.

## Requirements

Read `setup/requirements.md` for the full specification.

### Key Features
- Parse MOS6502 assembly syntax
- Support 11 addressing modes
- Output JSON with symbols and machine code
- Use the provided `opcodes.go` for opcode definitions

### API
```go
func Assemble(source string) (*AssemblerOutput, error)
```

### CLI
```
asm [-debug] <filename.asm>
```

### Addressing Modes
- Implied, Accumulator, Immediate
- Zero Page (X/Y indexed)
- Absolute (X/Y indexed)
- Indirect, Indexed Indirect, Indirect Indexed
- Relative

## Example Files

Test your implementation against:
- `setup/examples/` - Valid assembly files
- `setup/examples_invalid/` - Invalid files (should produce errors)

## Success Criteria

1. The program compiles
2. `simple_load.asm` assembles correctly
3. Output is valid JSON with correct structure
4. At least 5 example files assemble without errors
