package par2

import "encoding/binary"

// mulAddTables computes dst ^= factor * src over 16-bit little-endian words,
// using the precomputed tables for that factor. It dispatches to a SIMD kernel
// where one is available and falls back to the portable path otherwise.
func mulAddTables(dst, src []byte, t *factorTables) {
	if n := simdMulAdd(dst, src, t); n > 0 {
		dst, src = dst[n:], src[n:]
	}
	if len(src) == 0 {
		return
	}
	if simdEnabled {
		// Only the tail the kernel could not take is left, at most a handful of
		// words, so the log-based form is cheaper than building tables for it.
		mulAddLog(dst, src, t.factor)
		return
	}
	mulAddScalar(dst, src, &t.byteTables)
}

// mulAddLog is the direct log-add-antilog form, used for short runs.
func mulAddLog(dst, src []byte, factor uint16) {
	lf := uint32(gfLog[factor])
	for i := 0; i+1 < len(src); i += 2 {
		v := binary.LittleEndian.Uint16(src[i:])
		if v == 0 {
			continue
		}
		p := gfExp[(uint32(gfLog[v])+lf)%gfLimit]
		binary.LittleEndian.PutUint16(dst[i:], binary.LittleEndian.Uint16(dst[i:])^p)
	}
}

// mulAddScalar is the portable implementation. It works four words at a time
// through 64-bit loads, which keeps the table lookups independent enough for
// the processor to overlap them.
func mulAddScalar(dst, src []byte, t *byteTables) {
	lo, hi := &t.lo, &t.hi
	n := len(src) &^ 7
	for i := 0; i < n; i += 8 {
		s := binary.LittleEndian.Uint64(src[i:])
		d := binary.LittleEndian.Uint64(dst[i:])
		p := uint64(lo[byte(s)]^hi[byte(s>>8)]) |
			uint64(lo[byte(s>>16)]^hi[byte(s>>24)])<<16 |
			uint64(lo[byte(s>>32)]^hi[byte(s>>40)])<<32 |
			uint64(lo[byte(s>>48)]^hi[byte(s>>56)])<<48
		binary.LittleEndian.PutUint64(dst[i:], d^p)
	}
	for i := n; i+1 < len(src); i += 2 {
		v := binary.LittleEndian.Uint16(src[i:])
		p := lo[byte(v)] ^ hi[v>>8]
		binary.LittleEndian.PutUint16(dst[i:], binary.LittleEndian.Uint16(dst[i:])^p)
	}
}

// mulAdd computes dst ^= factor * src, building the tables on the spot. It is
// the convenient form for one-off multiplications such as those in solve; the
// encoder builds its tables once and calls mulAddTables directly.
func mulAdd(dst, src []byte, factor uint16) {
	if factor == 0 {
		return
	}
	if factor == 1 {
		for i := range src {
			dst[i] ^= src[i]
		}
		return
	}
	mulAddTables(dst, src, makeTables(factor))
}

// scale computes buf = factor * buf.
func scale(buf []byte, factor uint16) {
	if factor == 1 {
		return
	}
	lf := uint32(gfLog[factor])
	for i := 0; i+1 < len(buf); i += 2 {
		v := binary.LittleEndian.Uint16(buf[i:])
		if v == 0 {
			continue
		}
		binary.LittleEndian.PutUint16(buf[i:], gfExp[(uint32(gfLog[v])+lf)%gfLimit])
	}
}
