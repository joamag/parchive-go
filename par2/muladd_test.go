package par2

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

// reference is the definition of the operation, written the slow obvious way:
// dst ^= factor * src, one 16-bit word at a time through a plain multiply.
func reference(dst, src []byte, factor uint16) {
	for i := 0; i+1 < len(src); i += 2 {
		v := binary.LittleEndian.Uint16(src[i:])
		p := gfMul(factor, v)
		binary.LittleEndian.PutUint16(dst[i:], binary.LittleEndian.Uint16(dst[i:])^p)
	}
}

// TestMulAddMatchesReference is the test that matters most in this package: if
// the accelerated kernels ever disagree with the definition, every recovery set
// this library writes is silently wrong.
func TestMulAddMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	// Sizes chosen around the 32-byte kernel stride so the tail path, the
	// empty case and unaligned remainders are all covered.
	sizes := []int{0, 2, 4, 30, 32, 34, 62, 64, 66, 126, 128, 1000, 4096, 65536}
	factors := []uint16{0, 1, 2, 3, 0x00FF, 0x0100, 0x1234, 0xABCD, 0xFFFF}

	for _, size := range sizes {
		src := make([]byte, size)
		rng.Read(src)
		base := make([]byte, size)
		rng.Read(base)

		for _, f := range factors {
			want := append([]byte(nil), base...)
			reference(want, src, f)

			got := append([]byte(nil), base...)
			mulAdd(got, src, f)
			if !bytes.Equal(got, want) {
				t.Fatalf("mulAdd disagrees with the reference, size=%d factor=%#x", size, f)
			}

			// And the portable path on its own, so a broken fallback cannot
			// hide behind a working SIMD kernel.
			if f != 0 {
				scalarGot := append([]byte(nil), base...)
				tb := &byteTables{}
				for i := 0; i < 256; i++ {
					tb.lo[i] = gfMul(f, uint16(i))
					tb.hi[i] = gfMul(f, uint16(i)<<8)
				}
				mulAddScalar(scalarGot, src, tb)
				if !bytes.Equal(scalarGot, want) {
					t.Fatalf("mulAddScalar disagrees with the reference, size=%d factor=%#x", size, f)
				}
			}
		}
	}
}

// TestSIMDMatchesScalar compares the two implementations directly, so the test
// fails loudly on a machine whose kernel is broken even if both happen to agree
// with a shared bug elsewhere.
func TestSIMDMatchesScalar(t *testing.T) {
	if !simdEnabled {
		t.Skip("no SIMD kernel on this architecture")
	}
	rng := rand.New(rand.NewSource(2))
	src := make([]byte, 1<<16)
	rng.Read(src)

	for _, f := range []uint16{2, 0x1234, 0xABCD, 0xFFFF} {
		tables := makeTables(f)

		simd := make([]byte, len(src))
		n := simdMulAdd(simd, src, tables)
		if n == 0 {
			t.Skip("SIMD kernel is disabled at runtime on this machine")
		}
		if n != len(src) {
			t.Fatalf("kernel consumed %d of %d bytes", n, len(src))
		}

		want := make([]byte, len(src))
		reference(want, src, f)
		if !bytes.Equal(simd, want) {
			t.Fatalf("SIMD kernel disagrees with the reference for factor %#x", f)
		}
	}
}

// TestMulAddIsItsOwnInverse is a property the whole repair path depends on.
func TestMulAddInverse(t *testing.T) {
	src := make([]byte, 4096)
	rand.New(rand.NewSource(3)).Read(src)
	dst := make([]byte, 4096)
	want := append([]byte(nil), dst...)

	mulAdd(dst, src, 0x1234)
	if bytes.Equal(dst, want) {
		t.Fatal("mulAdd had no effect")
	}
	mulAdd(dst, src, 0x1234)
	if !bytes.Equal(dst, want) {
		t.Fatal("applying mulAdd twice did not cancel out")
	}
}

func BenchmarkMulAddKernel(b *testing.B) {
	const n = 512 << 10
	src := make([]byte, n)
	dst := make([]byte, n)
	rand.New(rand.NewSource(9)).Read(src)
	tables := makeTables(0xABCD)
	b.SetBytes(n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mulAddTables(dst, src, tables)
	}
}
