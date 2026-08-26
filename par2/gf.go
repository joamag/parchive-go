package par2

import "fmt"

// ---------------------------------------------------------------------------
// GF(2^16) — generator 2, primitive polynomial 0x1100B (as mandated by PAR2).
// ---------------------------------------------------------------------------

const (
	gfPoly  = 0x1100B
	gfLimit = 0xFFFF // 65535 = 3 * 5 * 17 * 257
)

var (
	gfExp [gfLimit]uint16     // gfExp[k] = 2^k
	gfLog [gfLimit + 1]uint16 // gfLog[2^k] = k
)

func init() {
	x := uint32(1)
	for i := 0; i < gfLimit; i++ {
		gfExp[i] = uint16(x)
		gfLog[x] = uint16(i)
		x <<= 1
		if x&0x10000 != 0 {
			x ^= gfPoly
		}
	}
}

func gfMul(a, b uint16) uint16 {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[(uint32(gfLog[a])+uint32(gfLog[b]))%gfLimit]
}

func gfDiv(a, b uint16) uint16 {
	if a == 0 {
		return 0
	}
	return gfExp[(uint32(gfLog[a])+gfLimit-uint32(gfLog[b]))%gfLimit]
}

func gfPow(a uint16, n uint32) uint16 {
	if a == 0 {
		return 0
	}
	return gfExp[(uint64(gfLog[a])*uint64(n))%gfLimit]
}

func gcd(a, b uint32) uint32 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// MaxInputSlices is phi(65535): the number of usable RS constants.
const MaxInputSlices = 32768

// inputConstants assigns the RS constant of every input slice. PAR2 uses
// 2^k for successive k whose logarithm is coprime with 65535, so that every
// constant has full multiplicative order.
func inputConstants(n int) ([]uint16, error) {
	if n > MaxInputSlices {
		return nil, fmt.Errorf("par2: %d input slices exceeds limit of %d", n, MaxInputSlices)
	}
	out := make([]uint16, 0, n)
	for k := uint32(1); len(out) < n; k++ {
		if gcd(k, gfLimit) == 1 {
			out = append(out, gfExp[k])
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Multiplication tables
// ---------------------------------------------------------------------------

// byteTables multiplies a 16-bit value by a fixed factor with two lookups and
// an xor. Multiplication distributes over the bit halves of the value, so
// factor*v is factor*(low byte) xor factor*(high byte), and each half is a
// 256-entry table. It replaces the log-add-antilog sequence, which needs a
// modulo per value.
type byteTables struct{ lo, hi [256]uint16 }

// nibbleTables is the same idea taken to four bits, laid out as eight 16-byte
// lookup tables: for each nibble position, the low and then the high byte of
// factor*(nibble << 4k). Sixteen entries is exactly one SIMD register, which is
// what lets a single instruction multiply sixteen bytes at once.
type nibbleTables [128]byte

// factorTables carries whichever representation the active kernel needs. Only
// the tables that will actually be used are built: filling the 256-entry byte
// tables costs several hundred multiplications, which is wasted work when the
// SIMD kernel is going to consume everything but a sub-32-byte tail.
type factorTables struct {
	factor uint16
	byteTables
	nibble nibbleTables
}

func makeTables(factor uint16) *factorTables {
	t := &factorTables{factor: factor}
	if simdEnabled {
		for k := 0; k < 4; k++ {
			for n := 0; n < 16; n++ {
				p := gfMul(factor, uint16(n)<<(4*k))
				t.nibble[k*16+n] = byte(p)
				t.nibble[64+k*16+n] = byte(p >> 8)
			}
		}
		return t
	}
	for i := 0; i < 256; i++ {
		t.lo[i] = gfMul(factor, uint16(i))
		t.hi[i] = gfMul(factor, uint16(i)<<8)
	}
	return t
}
