package par2

import (
	"hash/crc32"
	"math/rand"
	"testing"
)

// TestRollingMatchesChecksum is the correctness anchor for the whole scan: the
// incrementally updated value must equal what hash/crc32 reports for the same
// window, at every offset.
func TestRollingMatchesChecksum(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	for _, width := range []int{1, 2, 4, 15, 16, 64, 512, 4096} {
		data := make([]byte, width*4+37)
		rng.Read(data)

		r := newRolling(width)
		raw := r.raw(data[:width])
		if got, want := r.checksum(raw), crc32.ChecksumIEEE(data[:width]); got != want {
			t.Fatalf("width %d offset 0: got %#x want %#x", width, got, want)
		}
		for off := 1; off+width <= len(data); off++ {
			raw = r.roll(raw, data[off-1], data[off+width-1])
			if got, want := r.checksum(raw), crc32.ChecksumIEEE(data[off:off+width]); got != want {
				t.Fatalf("width %d offset %d: got %#x want %#x", width, off, got, want)
			}
		}
	}
}

// The window is a slice, which can be megabytes, so A^n has to come from
// repeated squaring rather than n iterations. Check the two agree.
func TestAdvanceByMatchesIteration(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 7, 8, 100, 1000} {
		m := advanceBy(uint64(n))
		for _, x := range []uint32{1, 0x80000000, 0xDEADBEEF, 0xFFFFFFFF} {
			want := x
			for i := 0; i < n; i++ {
				want = crcStep(want, 0)
			}
			if got := m.apply(x); got != want {
				t.Fatalf("A^%d(%#x) = %#x, want %#x", n, x, got, want)
			}
		}
	}
}

// The rolling derivation relies on the CRC table being linear over GF(2).
func TestCRCTableIsLinear(t *testing.T) {
	for a := 0; a < 256; a += 7 {
		for b := 0; b < 256; b += 11 {
			if crcTable[a^b] != crcTable[a]^crcTable[b] {
				t.Fatalf("table is not linear at %d,%d", a, b)
			}
		}
	}
}

func BenchmarkRoll(b *testing.B) {
	const width = 4096
	data := make([]byte, 1<<20)
	rand.New(rand.NewSource(5)).Read(data)
	r := newRolling(width)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		raw := r.raw(data[:width])
		for off := 1; off+width <= len(data); off++ {
			raw = r.roll(raw, data[off-1], data[off+width-1])
		}
	}
}
