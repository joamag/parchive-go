package par2

import "hash/crc32"

// Rolling CRC32 over a fixed-width window.
//
// PAR2 records a CRC32 per input slice, which is what makes it possible to find
// a slice that has drifted away from its natural offset: slide a window of one
// slice across the file and watch for a CRC that is in the set. Recomputing the
// CRC at every byte would be quadratic, so it is updated incrementally instead.
//
// The IEEE CRC register update is affine over GF(2):
//
//	step(r, b) = table[(r^b)&0xFF] ^ (r>>8) = A(r) ^ table[b]
//
// where A is the linear map that advances the register by one zero byte. From
// that, if C is the register for the window starting at i (counted from a zero
// register), the next window is
//
//	C' = step(C, incoming) ^ A^n(table[outgoing])
//
// so the only precomputation needed is A^n applied to each of the 256 possible
// outgoing bytes, plus a constant that folds in the initial and final
// complements that hash/crc32 applies.

var crcTable = crc32.MakeTable(crc32.IEEE)

// crcStep advances the raw register by one byte.
func crcStep(r uint32, b byte) uint32 {
	return crcTable[byte(r)^b] ^ (r >> 8)
}

// A linear map on 32-bit vectors, stored as the images of the basis vectors:
// m[i] is where bit i goes. Composing and exponentiating these is how A^n is
// obtained for a window of millions of bytes without iterating over them.
type crcMatrix [32]uint32

func (m *crcMatrix) apply(x uint32) uint32 {
	var out uint32
	for i := 0; x != 0; i, x = i+1, x>>1 {
		if x&1 != 0 {
			out ^= m[i]
		}
	}
	return out
}

func (m *crcMatrix) mul(n *crcMatrix) crcMatrix {
	var out crcMatrix
	for i := range out {
		out[i] = m.apply(n[i])
	}
	return out
}

// advanceOne is A itself: the register moved on by a single zero byte.
func advanceOne() crcMatrix {
	var m crcMatrix
	for i := range m {
		m[i] = crcStep(1<<uint(i), 0)
	}
	return m
}

// advanceBy returns A^n by repeated squaring.
func advanceBy(n uint64) crcMatrix {
	var out crcMatrix
	for i := range out {
		out[i] = 1 << uint(i) // identity
	}
	base := advanceOne()
	for n > 0 {
		if n&1 != 0 {
			out = base.mul(&out)
		}
		base = base.mul(&base)
		n >>= 1
	}
	return out
}

// rolling holds the precomputation for one window width.
type rolling struct {
	width  int
	window [256]uint32 // A^n(table[b]) for each outgoing byte
	mask   uint32      // folds the initial and final complements back in
}

func newRolling(width int) *rolling {
	r := &rolling{width: width}
	an := advanceBy(uint64(width))
	for b := 0; b < 256; b++ {
		r.window[b] = an.apply(crcTable[b])
	}
	r.mask = an.apply(0xFFFFFFFF) ^ 0xFFFFFFFF
	return r
}

// raw returns the zero-initialised register for a block, which is the form the
// rolling update works with.
func (r *rolling) raw(block []byte) uint32 {
	var c uint32
	for _, b := range block {
		c = crcStep(c, b)
	}
	return c
}

// checksum converts a raw register into the value hash/crc32 would report.
func (r *rolling) checksum(raw uint32) uint32 { return raw ^ r.mask }

// roll moves the window one byte to the right.
func (r *rolling) roll(raw uint32, outgoing, incoming byte) uint32 {
	return crcStep(raw, incoming) ^ r.window[outgoing]
}
