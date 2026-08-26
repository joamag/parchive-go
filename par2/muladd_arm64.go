package par2

// Every ARMv8 processor has NEON, so the kernel needs no runtime probing.
const simdEnabled = true

//go:noescape
func mulAddNEON(dst, src *byte, n int, tables *nibbleTables)

// simdMulAdd applies the SIMD kernel to the 32-byte aligned prefix of src and
// reports how many bytes it consumed.
func simdMulAdd(dst, src []byte, t *factorTables) int {
	n := len(src) &^ 31
	if n == 0 {
		return 0
	}
	mulAddNEON(&dst[0], &src[0], n, &t.nibble)
	return n
}
