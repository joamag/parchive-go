#include "textflag.h"

DATA lowNibble<>+0x00(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA lowNibble<>+0x08(SB)/8, $0x0f0f0f0f0f0f0f0f
GLOBL lowNibble<>(SB), RODATA, $16

// Gathers the even bytes into the low half and the odd bytes into the high
// half, which separates the low and high byte planes of each 16-bit value.
DATA deint<>+0x00(SB)/8, $0x0e0c0a0806040200
DATA deint<>+0x08(SB)/8, $0x0f0d0b0907050301
GLOBL deint<>(SB), RODATA, $16

// hasSSSE3() bool
TEXT ·hasSSSE3(SB), NOSPLIT, $0-1
	MOVQ BX, R8 // CPUID clobbers BX
	MOVL $1, AX
	CPUID
	MOVQ R8, BX
	SHRL $9, CX
	ANDL $1, CX
	MOVB CX, ret+0(FP)
	RET

// mulAddSSSE3(dst, src *byte, n int, tables *nibbleTables)
//
// dst ^= factor * src over GF(2^16), 32 bytes (16 values) per iteration. Each
// value is split into four nibbles; PSHUFB turns a 16-entry table into sixteen
// parallel lookups, and the four partial products are XORed together.
TEXT ·mulAddSSSE3(SB), NOSPLIT, $0-32
	MOVQ dst+0(FP),    DI
	MOVQ src+8(FP),    SI
	MOVQ n+16(FP),     CX
	MOVQ tables+24(FP), AX

	MOVOU lowNibble<>(SB), X0
	MOVOU deint<>(SB),     X1
	ADDQ  SI, CX            // CX = end of src

loop:
	// split the source into low and high byte planes
	MOVOU  0(SI), X2
	MOVOU  16(SI), X3
	PSHUFB X1, X2
	PSHUFB X1, X3
	MOVOA  X2, X4
	PUNPCKLQDQ X3, X4       // X4 = 16 low bytes
	PUNPCKHQDQ X3, X2       // X2 = 16 high bytes

	// four nibble index vectors
	MOVOA X4, X5
	PAND  X0, X5            // n0 = lo & 0x0F
	MOVOA X4, X6
	PSRLW $4, X6
	PAND  X0, X6            // n1 = (lo >> 4) & 0x0F
	MOVOA X2, X7
	PAND  X0, X7            // n2 = hi & 0x0F
	MOVOA X2, X8
	PSRLW $4, X8
	PAND  X0, X8            // n3 = (hi >> 4) & 0x0F

	// low byte plane of the product
	MOVOU 0(AX), X11
	PSHUFB X5, X11
	MOVOA X11, X9
	MOVOU 16(AX), X11
	PSHUFB X6, X11
	PXOR  X11, X9
	MOVOU 32(AX), X11
	PSHUFB X7, X11
	PXOR  X11, X9
	MOVOU 48(AX), X11
	PSHUFB X8, X11
	PXOR  X11, X9

	// high byte plane of the product
	MOVOU 64(AX), X11
	PSHUFB X5, X11
	MOVOA X11, X10
	MOVOU 80(AX), X11
	PSHUFB X6, X11
	PXOR  X11, X10
	MOVOU 96(AX), X11
	PSHUFB X7, X11
	PXOR  X11, X10
	MOVOU 112(AX), X11
	PSHUFB X8, X11
	PXOR  X11, X10

	// accumulate into the destination, split the same way
	MOVOU  0(DI), X12
	MOVOU  16(DI), X13
	PSHUFB X1, X12
	PSHUFB X1, X13
	MOVOA  X12, X14
	PUNPCKLQDQ X13, X14     // dst low bytes
	PUNPCKHQDQ X13, X12     // dst high bytes
	PXOR   X14, X9
	PXOR   X12, X10

	// interleave the planes back into 16-bit words and store
	MOVOA X9, X15
	PUNPCKLBW X10, X15
	PUNPCKHBW X10, X9
	MOVOU X15, 0(DI)
	MOVOU X9, 16(DI)

	ADDQ $32, SI
	ADDQ $32, DI
	CMPQ SI, CX
	JLT  loop
	RET
