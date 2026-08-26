package par2

const simdEnabled = true

//go:noescape
func mulAddSSSE3(dst, src *byte, n int, tables *nibbleTables)

//go:noescape
func hasSSSE3() bool

// SSSE3 arrived in 2006 and PSHUFB is what makes the nibble tables worth using,
// so anything older falls back to the portable path.
var useSSSE3 = hasSSSE3()

func simdMulAdd(dst, src []byte, t *factorTables) int {
	if !useSSSE3 {
		return 0
	}
	n := len(src) &^ 31
	if n == 0 {
		return 0
	}
	mulAddSSSE3(&dst[0], &src[0], n, &t.nibble)
	return n
}
