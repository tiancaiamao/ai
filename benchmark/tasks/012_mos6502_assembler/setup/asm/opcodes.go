// Package asm provides a MOS6502 assembler.
// This file contains instruction definitions: opcodes, addressing modes, sizes, and cycle counts.

package asm

import (
	"fmt"
	"strings"
)

// AddrMode represents a 6502 addressing mode.
type AddrMode int

const (
	AddrImplied         AddrMode = iota // no operand (NOP, RTS, TAX)
	AddrAccumulator                     // A register (ASL A, ROL A)
	AddrImmediate                       // #$XX
	AddrZeroPage                        // $XX
	AddrZeroPageX                       // $XX,X
	AddrZeroPageY                       // $XX,Y
	AddrAbsolute                        // $XXXX
	AddrAbsoluteX                       // $XXXX,X
	AddrAbsoluteY                       // $XXXX,Y
	AddrIndirect                        // ($XXXX) - JMP only
	AddrIndexedIndirect                 // ($XX,X)
	AddrIndirectIndexed                 // ($XX),Y
	AddrRelative                        // branches: PC + offset
)

// Mnemonic represents a 6502 instruction mnemonic.
type Mnemonic int

const (
	MnemonicIllegal Mnemonic = iota // Illegal/undocumented opcode
	ADC
	AND
	ASL
	BCC
	BCS
	BEQ
	BIT
	BMI
	BNE
	BPL
	BRK
	BVC
	BVS
	CLC
	CLD
	CLI
	CLV
	CMP
	CPX
	CPY
	DEC
	DEX
	DEY
	EOR
	INC
	INX
	INY
	JMP
	JSR
	LDA
	LDX
	LDY
	LSR
	NOP
	ORA
	PHA
	PHP
	PLA
	PLP
	ROL
	ROR
	RTI
	RTS
	SBC
	SEC
	SED
	SEI
	STA
	STX
	STY
	TAX
	TAY
	TSX
	TXA
	TXS
	TYA
)

var mnemonicNames = [...]string{
	MnemonicIllegal: "",
	ADC:             "ADC",
	AND:             "AND",
	ASL:             "ASL",
	BCC:             "BCC",
	BCS:             "BCS",
	BEQ:             "BEQ",
	BIT:             "BIT",
	BMI:             "BMI",
	BNE:             "BNE",
	BPL:             "BPL",
	BRK:             "BRK",
	BVC:             "BVC",
	BVS:             "BVS",
	CLC:             "CLC",
	CLD:             "CLD",
	CLI:             "CLI",
	CLV:             "CLV",
	CMP:             "CMP",
	CPX:             "CPX",
	CPY:             "CPY",
	DEC:             "DEC",
	DEX:             "DEX",
	DEY:             "DEY",
	EOR:             "EOR",
	INC:             "INC",
	INX:             "INX",
	INY:             "INY",
	JMP:             "JMP",
	JSR:             "JSR",
	LDA:             "LDA",
	LDX:             "LDX",
	LDY:             "LDY",
	LSR:             "LSR",
	NOP:             "NOP",
	ORA:             "ORA",
	PHA:             "PHA",
	PHP:             "PHP",
	PLA:             "PLA",
	PLP:             "PLP",
	ROL:             "ROL",
	ROR:             "ROR",
	RTI:             "RTI",
	RTS:             "RTS",
	SBC:             "SBC",
	SEC:             "SEC",
	SED:             "SED",
	SEI:             "SEI",
	STA:             "STA",
	STX:             "STX",
	STY:             "STY",
	TAX:             "TAX",
	TAY:             "TAY",
	TSX:             "TSX",
	TXA:             "TXA",
	TXS:             "TXS",
	TYA:             "TYA",
}

// String returns the string representation of the mnemonic.
func (m Mnemonic) String() string {
	if int(m) < len(mnemonicNames) {
		return mnemonicNames[m]
	}
	return ""
}

// OpcodeDef defines a 6502 instruction.
type OpcodeDef struct {
	Opcode    byte     // The opcode byte (0x00-0xFF)
	Op        Mnemonic // Instruction mnemonic (MnemonicIllegal = illegal opcode)
	Mode      AddrMode // Addressing mode
	Size      int      // Instruction size in bytes (1, 2, or 3)
	Cycles    int      // Base cycles (without page-cross penalty)
	PageCross bool     // +1 cycle if page boundary crossed
}

// IsIllegal returns true if this is an illegal/undocumented opcode.
func (o OpcodeDef) IsIllegal() bool {
	return o.Op == MnemonicIllegal
}

// OperandSize returns the operand size in bytes (Size - 1).
func (o OpcodeDef) OperandSize() int {
	if o.Size > 0 {
		return o.Size - 1
	}
	return 0
}

// FormatOperand formats the operand bytes according to the addressing mode.
func (o OpcodeDef) FormatOperand(operand []byte) string {
	switch o.Mode {
	case AddrImplied:
		return ""
	case AddrAccumulator:
		return " A"
	case AddrImmediate:
		if len(operand) >= 1 {
			return fmt.Sprintf(" #%#x", operand[0])
		}
	case AddrZeroPage:
		if len(operand) >= 1 {
			return fmt.Sprintf(" %#x", operand[0])
		}
	case AddrZeroPageX:
		if len(operand) >= 1 {
			return fmt.Sprintf(" %#x,X", operand[0])
		}
	case AddrZeroPageY:
		if len(operand) >= 1 {
			return fmt.Sprintf(" %#x,Y", operand[0])
		}
	case AddrAbsolute:
		if len(operand) >= 2 {
			addr := int(operand[0]) + (int(operand[1]) << 8)
			return fmt.Sprintf(" %#x", addr)
		}
	case AddrAbsoluteX:
		if len(operand) >= 2 {
			addr := int(operand[0]) + (int(operand[1]) << 8)
			return fmt.Sprintf(" %#x,X", addr)
		}
	case AddrAbsoluteY:
		if len(operand) >= 2 {
			addr := int(operand[0]) + (int(operand[1]) << 8)
			return fmt.Sprintf(" %#x,Y", addr)
		}
	case AddrIndirect:
		if len(operand) >= 2 {
			addr := int(operand[0]) + (int(operand[1]) << 8)
			return fmt.Sprintf(" (%#x)", addr)
		}
	case AddrIndexedIndirect:
		if len(operand) >= 1 {
			return fmt.Sprintf(" (%#x,X)", operand[0])
		}
	case AddrIndirectIndexed:
		if len(operand) >= 1 {
			return fmt.Sprintf(" (%#x),Y", operand[0])
		}
	case AddrRelative:
		if len(operand) >= 1 {
			if operand[0] > 0x7f {
				return fmt.Sprintf(" -%#x", 256-int(operand[0]))
			}
			return fmt.Sprintf(" +%#x", operand[0])
		}
	}
	return ""
}

// Opcodes is the master table of all 256 6502 opcodes.
// Illegal/undocumented opcodes have MnemonicIllegal as their Op.
var Opcodes = [256]OpcodeDef{
	// 0x00-0x0F
	0x00: {0x00, BRK, AddrImplied, 1, 7, false},
	0x01: {0x01, ORA, AddrIndexedIndirect, 2, 6, false},
	0x02: {0x02, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x03: {0x03, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x04: {0x04, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x05: {0x05, ORA, AddrZeroPage, 2, 3, false},
	0x06: {0x06, ASL, AddrZeroPage, 2, 5, false},
	0x07: {0x07, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x08: {0x08, PHP, AddrImplied, 1, 3, false},
	0x09: {0x09, ORA, AddrImmediate, 2, 2, false},
	0x0A: {0x0A, ASL, AddrAccumulator, 1, 2, false},
	0x0B: {0x0B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x0C: {0x0C, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x0D: {0x0D, ORA, AddrAbsolute, 3, 4, false},
	0x0E: {0x0E, ASL, AddrAbsolute, 3, 6, false},
	0x0F: {0x0F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x10-0x1F
	0x10: {0x10, BPL, AddrRelative, 2, 2, true},
	0x11: {0x11, ORA, AddrIndirectIndexed, 2, 5, true},
	0x12: {0x12, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x13: {0x13, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x14: {0x14, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x15: {0x15, ORA, AddrZeroPageX, 2, 4, false},
	0x16: {0x16, ASL, AddrZeroPageX, 2, 6, false},
	0x17: {0x17, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x18: {0x18, CLC, AddrImplied, 1, 2, false},
	0x19: {0x19, ORA, AddrAbsoluteY, 3, 4, true},
	0x1A: {0x1A, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x1B: {0x1B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x1C: {0x1C, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x1D: {0x1D, ORA, AddrAbsoluteX, 3, 4, true},
	0x1E: {0x1E, ASL, AddrAbsoluteX, 3, 7, false},
	0x1F: {0x1F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x20-0x2F
	0x20: {0x20, JSR, AddrAbsolute, 3, 6, false},
	0x21: {0x21, AND, AddrIndexedIndirect, 2, 6, false},
	0x22: {0x22, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x23: {0x23, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x24: {0x24, BIT, AddrZeroPage, 2, 3, false},
	0x25: {0x25, AND, AddrZeroPage, 2, 3, false},
	0x26: {0x26, ROL, AddrZeroPage, 2, 5, false},
	0x27: {0x27, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x28: {0x28, PLP, AddrImplied, 1, 4, false},
	0x29: {0x29, AND, AddrImmediate, 2, 2, false},
	0x2A: {0x2A, ROL, AddrAccumulator, 1, 2, false},
	0x2B: {0x2B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x2C: {0x2C, BIT, AddrAbsolute, 3, 4, false},
	0x2D: {0x2D, AND, AddrAbsolute, 3, 4, false},
	0x2E: {0x2E, ROL, AddrAbsolute, 3, 6, false},
	0x2F: {0x2F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x30-0x3F
	0x30: {0x30, BMI, AddrRelative, 2, 2, true},
	0x31: {0x31, AND, AddrIndirectIndexed, 2, 5, true},
	0x32: {0x32, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x33: {0x33, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x34: {0x34, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x35: {0x35, AND, AddrZeroPageX, 2, 4, false},
	0x36: {0x36, ROL, AddrZeroPageX, 2, 6, false},
	0x37: {0x37, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x38: {0x38, SEC, AddrImplied, 1, 2, false},
	0x39: {0x39, AND, AddrAbsoluteY, 3, 4, true},
	0x3A: {0x3A, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x3B: {0x3B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x3C: {0x3C, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x3D: {0x3D, AND, AddrAbsoluteX, 3, 4, true},
	0x3E: {0x3E, ROL, AddrAbsoluteX, 3, 7, false},
	0x3F: {0x3F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x40-0x4F
	0x40: {0x40, RTI, AddrImplied, 1, 6, false},
	0x41: {0x41, EOR, AddrIndexedIndirect, 2, 6, false},
	0x42: {0x42, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x43: {0x43, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x44: {0x44, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x45: {0x45, EOR, AddrZeroPage, 2, 3, false},
	0x46: {0x46, LSR, AddrZeroPage, 2, 5, false},
	0x47: {0x47, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x48: {0x48, PHA, AddrImplied, 1, 3, false},
	0x49: {0x49, EOR, AddrImmediate, 2, 2, false},
	0x4A: {0x4A, LSR, AddrAccumulator, 1, 2, false},
	0x4B: {0x4B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x4C: {0x4C, JMP, AddrAbsolute, 3, 3, false},
	0x4D: {0x4D, EOR, AddrAbsolute, 3, 4, false},
	0x4E: {0x4E, LSR, AddrAbsolute, 3, 6, false},
	0x4F: {0x4F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x50-0x5F
	0x50: {0x50, BVC, AddrRelative, 2, 2, true},
	0x51: {0x51, EOR, AddrIndirectIndexed, 2, 5, true},
	0x52: {0x52, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x53: {0x53, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x54: {0x54, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x55: {0x55, EOR, AddrZeroPageX, 2, 4, false},
	0x56: {0x56, LSR, AddrZeroPageX, 2, 6, false},
	0x57: {0x57, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x58: {0x58, CLI, AddrImplied, 1, 2, false},
	0x59: {0x59, EOR, AddrAbsoluteY, 3, 4, true},
	0x5A: {0x5A, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x5B: {0x5B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x5C: {0x5C, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x5D: {0x5D, EOR, AddrAbsoluteX, 3, 4, true},
	0x5E: {0x5E, LSR, AddrAbsoluteX, 3, 7, false},
	0x5F: {0x5F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x60-0x6F
	0x60: {0x60, RTS, AddrImplied, 1, 6, false},
	0x61: {0x61, ADC, AddrIndexedIndirect, 2, 6, false},
	0x62: {0x62, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x63: {0x63, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x64: {0x64, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x65: {0x65, ADC, AddrZeroPage, 2, 3, false},
	0x66: {0x66, ROR, AddrZeroPage, 2, 5, false},
	0x67: {0x67, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x68: {0x68, PLA, AddrImplied, 1, 4, false},
	0x69: {0x69, ADC, AddrImmediate, 2, 2, false},
	0x6A: {0x6A, ROR, AddrAccumulator, 1, 2, false},
	0x6B: {0x6B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x6C: {0x6C, JMP, AddrIndirect, 3, 5, false},
	0x6D: {0x6D, ADC, AddrAbsolute, 3, 4, false},
	0x6E: {0x6E, ROR, AddrAbsolute, 3, 6, false},
	0x6F: {0x6F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x70-0x7F
	0x70: {0x70, BVS, AddrRelative, 2, 2, true},
	0x71: {0x71, ADC, AddrIndirectIndexed, 2, 5, true},
	0x72: {0x72, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x73: {0x73, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x74: {0x74, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x75: {0x75, ADC, AddrZeroPageX, 2, 4, false},
	0x76: {0x76, ROR, AddrZeroPageX, 2, 6, false},
	0x77: {0x77, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x78: {0x78, SEI, AddrImplied, 1, 2, false},
	0x79: {0x79, ADC, AddrAbsoluteY, 3, 4, true},
	0x7A: {0x7A, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x7B: {0x7B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x7C: {0x7C, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x7D: {0x7D, ADC, AddrAbsoluteX, 3, 4, true},
	0x7E: {0x7E, ROR, AddrAbsoluteX, 3, 7, false},
	0x7F: {0x7F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x80-0x8F
	0x80: {0x80, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x81: {0x81, STA, AddrIndexedIndirect, 2, 6, false},
	0x82: {0x82, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x83: {0x83, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x84: {0x84, STY, AddrZeroPage, 2, 3, false},
	0x85: {0x85, STA, AddrZeroPage, 2, 3, false},
	0x86: {0x86, STX, AddrZeroPage, 2, 3, false},
	0x87: {0x87, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x88: {0x88, DEY, AddrImplied, 1, 2, false},
	0x89: {0x89, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x8A: {0x8A, TXA, AddrImplied, 1, 2, false},
	0x8B: {0x8B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x8C: {0x8C, STY, AddrAbsolute, 3, 4, false},
	0x8D: {0x8D, STA, AddrAbsolute, 3, 4, false},
	0x8E: {0x8E, STX, AddrAbsolute, 3, 4, false},
	0x8F: {0x8F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0x90-0x9F
	0x90: {0x90, BCC, AddrRelative, 2, 2, true},
	0x91: {0x91, STA, AddrIndirectIndexed, 2, 6, false},
	0x92: {0x92, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x93: {0x93, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x94: {0x94, STY, AddrZeroPageX, 2, 4, false},
	0x95: {0x95, STA, AddrZeroPageX, 2, 4, false},
	0x96: {0x96, STX, AddrZeroPageY, 2, 4, false},
	0x97: {0x97, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x98: {0x98, TYA, AddrImplied, 1, 2, false},
	0x99: {0x99, STA, AddrAbsoluteY, 3, 5, false},
	0x9A: {0x9A, TXS, AddrImplied, 1, 2, false},
	0x9B: {0x9B, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x9C: {0x9C, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x9D: {0x9D, STA, AddrAbsoluteX, 3, 5, false},
	0x9E: {0x9E, MnemonicIllegal, AddrImplied, 1, 0, false},
	0x9F: {0x9F, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0xA0-0xAF
	0xA0: {0xA0, LDY, AddrImmediate, 2, 2, false},
	0xA1: {0xA1, LDA, AddrIndexedIndirect, 2, 6, false},
	0xA2: {0xA2, LDX, AddrImmediate, 2, 2, false},
	0xA3: {0xA3, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xA4: {0xA4, LDY, AddrZeroPage, 2, 3, false},
	0xA5: {0xA5, LDA, AddrZeroPage, 2, 3, false},
	0xA6: {0xA6, LDX, AddrZeroPage, 2, 3, false},
	0xA7: {0xA7, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xA8: {0xA8, TAY, AddrImplied, 1, 2, false},
	0xA9: {0xA9, LDA, AddrImmediate, 2, 2, false},
	0xAA: {0xAA, TAX, AddrImplied, 1, 2, false},
	0xAB: {0xAB, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xAC: {0xAC, LDY, AddrAbsolute, 3, 4, false},
	0xAD: {0xAD, LDA, AddrAbsolute, 3, 4, false},
	0xAE: {0xAE, LDX, AddrAbsolute, 3, 4, false},
	0xAF: {0xAF, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0xB0-0xBF
	0xB0: {0xB0, BCS, AddrRelative, 2, 2, true},
	0xB1: {0xB1, LDA, AddrIndirectIndexed, 2, 5, true},
	0xB2: {0xB2, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xB3: {0xB3, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xB4: {0xB4, LDY, AddrZeroPageX, 2, 4, false},
	0xB5: {0xB5, LDA, AddrZeroPageX, 2, 4, false},
	0xB6: {0xB6, LDX, AddrZeroPageY, 2, 4, false},
	0xB7: {0xB7, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xB8: {0xB8, CLV, AddrImplied, 1, 2, false},
	0xB9: {0xB9, LDA, AddrAbsoluteY, 3, 4, true},
	0xBA: {0xBA, TSX, AddrImplied, 1, 2, false},
	0xBB: {0xBB, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xBC: {0xBC, LDY, AddrAbsoluteX, 3, 4, true},
	0xBD: {0xBD, LDA, AddrAbsoluteX, 3, 4, true},
	0xBE: {0xBE, LDX, AddrAbsoluteY, 3, 4, true},
	0xBF: {0xBF, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0xC0-0xCF
	0xC0: {0xC0, CPY, AddrImmediate, 2, 2, false},
	0xC1: {0xC1, CMP, AddrIndexedIndirect, 2, 6, false},
	0xC2: {0xC2, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xC3: {0xC3, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xC4: {0xC4, CPY, AddrZeroPage, 2, 3, false},
	0xC5: {0xC5, CMP, AddrZeroPage, 2, 3, false},
	0xC6: {0xC6, DEC, AddrZeroPage, 2, 5, false},
	0xC7: {0xC7, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xC8: {0xC8, INY, AddrImplied, 1, 2, false},
	0xC9: {0xC9, CMP, AddrImmediate, 2, 2, false},
	0xCA: {0xCA, DEX, AddrImplied, 1, 2, false},
	0xCB: {0xCB, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xCC: {0xCC, CPY, AddrAbsolute, 3, 4, false},
	0xCD: {0xCD, CMP, AddrAbsolute, 3, 4, false},
	0xCE: {0xCE, DEC, AddrAbsolute, 3, 6, false},
	0xCF: {0xCF, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0xD0-0xDF
	0xD0: {0xD0, BNE, AddrRelative, 2, 2, true},
	0xD1: {0xD1, CMP, AddrIndirectIndexed, 2, 5, true},
	0xD2: {0xD2, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xD3: {0xD3, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xD4: {0xD4, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xD5: {0xD5, CMP, AddrZeroPageX, 2, 4, false},
	0xD6: {0xD6, DEC, AddrZeroPageX, 2, 6, false},
	0xD7: {0xD7, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xD8: {0xD8, CLD, AddrImplied, 1, 2, false},
	0xD9: {0xD9, CMP, AddrAbsoluteY, 3, 4, true},
	0xDA: {0xDA, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xDB: {0xDB, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xDC: {0xDC, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xDD: {0xDD, CMP, AddrAbsoluteX, 3, 4, true},
	0xDE: {0xDE, DEC, AddrAbsoluteX, 3, 7, false},
	0xDF: {0xDF, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0xE0-0xEF
	0xE0: {0xE0, CPX, AddrImmediate, 2, 2, false},
	0xE1: {0xE1, SBC, AddrIndexedIndirect, 2, 6, false},
	0xE2: {0xE2, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xE3: {0xE3, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xE4: {0xE4, CPX, AddrZeroPage, 2, 3, false},
	0xE5: {0xE5, SBC, AddrZeroPage, 2, 3, false},
	0xE6: {0xE6, INC, AddrZeroPage, 2, 5, false},
	0xE7: {0xE7, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xE8: {0xE8, INX, AddrImplied, 1, 2, false},
	0xE9: {0xE9, SBC, AddrImmediate, 2, 2, false},
	0xEA: {0xEA, NOP, AddrImplied, 1, 2, false},
	0xEB: {0xEB, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xEC: {0xEC, CPX, AddrAbsolute, 3, 4, false},
	0xED: {0xED, SBC, AddrAbsolute, 3, 4, false},
	0xEE: {0xEE, INC, AddrAbsolute, 3, 6, false},
	0xEF: {0xEF, MnemonicIllegal, AddrImplied, 1, 0, false},

	// 0xF0-0xFF
	0xF0: {0xF0, BEQ, AddrRelative, 2, 2, true},
	0xF1: {0xF1, SBC, AddrIndirectIndexed, 2, 5, true},
	0xF2: {0xF2, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xF3: {0xF3, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xF4: {0xF4, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xF5: {0xF5, SBC, AddrZeroPageX, 2, 4, false},
	0xF6: {0xF6, INC, AddrZeroPageX, 2, 6, false},
	0xF7: {0xF7, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xF8: {0xF8, SED, AddrImplied, 1, 2, false},
	0xF9: {0xF9, SBC, AddrAbsoluteY, 3, 4, true},
	0xFA: {0xFA, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xFB: {0xFB, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xFC: {0xFC, MnemonicIllegal, AddrImplied, 1, 0, false},
	0xFD: {0xFD, SBC, AddrAbsoluteX, 3, 4, true},
	0xFE: {0xFE, INC, AddrAbsoluteX, 3, 7, false},
	0xFF: {0xFF, MnemonicIllegal, AddrImplied, 1, 0, false},
}

// FindOpcode finds an opcode by mnemonic string and addressing mode.
// The mnemonic string is case insensitive.
// Returns the opcode definition and true if found, or nil and false if not found.
func FindOpcode(mnemonic string, mode AddrMode) (*OpcodeDef, bool) {
	upper := strings.ToUpper(mnemonic)
	for i := 0; i < 256; i++ {
		if Opcodes[i].Op.String() == upper && Opcodes[i].Mode == mode {
			return &Opcodes[i], true
		}
	}
	return nil, false
}
