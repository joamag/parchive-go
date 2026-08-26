#include "textflag.h"

// mulAddNEON(dst, src *byte, n int, tables *nibbleTables)
//
// dst ^= factor * src over GF(2^16), 32 bytes (16 values) per iteration.
// Every 16-bit value is split into four nibbles; each nibble indexes a 16-entry
// table through VTBL, and the four partial products are XORed together. VLD2
// deinterleaves the low and high byte planes on the way in, VST2 puts them back.
TEXT ·mulAddNEON(SB), NOSPLIT, $0-32
	MOVD dst+0(FP),    R0
	MOVD src+8(FP),    R1
	MOVD n+16(FP),     R2
	MOVD tables+24(FP), R3

	// V8..V11  low byte tables for nibbles 0..3
	// V12..V15 high byte tables for nibbles 0..3
	VLD1.P 64(R3), [V8.B16, V9.B16, V10.B16, V11.B16]
	VLD1   (R3),   [V12.B16, V13.B16, V14.B16, V15.B16]

	// V7 = 0x0F broadcast, to isolate the low nibble of each byte
	MOVD   $0x0F, R4
	VDUP   R4, V7.B16

	ADD  R1, R2, R5    // R5 = end of src

loop:
	VLD2.P 32(R1), [V0.B16, V1.B16]    // V0 low bytes, V1 high bytes

	VAND  V7.B16, V0.B16, V2.B16       // n0 = lo & 0x0F
	VUSHR $4, V0.B16, V3.B16           // n1 = lo >> 4
	VAND  V7.B16, V1.B16, V4.B16       // n2 = hi & 0x0F
	VUSHR $4, V1.B16, V5.B16           // n3 = hi >> 4

	// low byte plane of the product
	VTBL V2.B16, [V8.B16],  V16.B16
	VTBL V3.B16, [V9.B16],  V17.B16
	VTBL V4.B16, [V10.B16], V18.B16
	VTBL V5.B16, [V11.B16], V19.B16
	VEOR V17.B16, V16.B16, V16.B16
	VEOR V19.B16, V18.B16, V18.B16
	VEOR V18.B16, V16.B16, V16.B16

	// high byte plane of the product
	VTBL V2.B16, [V12.B16], V20.B16
	VTBL V3.B16, [V13.B16], V21.B16
	VTBL V4.B16, [V14.B16], V22.B16
	VTBL V5.B16, [V15.B16], V23.B16
	VEOR V21.B16, V20.B16, V20.B16
	VEOR V23.B16, V22.B16, V22.B16
	VEOR V22.B16, V20.B16, V20.B16

	// accumulate into dst, read back without advancing the pointer
	VLD2 (R0), [V24.B16, V25.B16]
	VEOR V16.B16, V24.B16, V24.B16
	VEOR V20.B16, V25.B16, V25.B16
	VST2.P [V24.B16, V25.B16], 32(R0)

	CMP  R5, R1
	BLT  loop
	RET
