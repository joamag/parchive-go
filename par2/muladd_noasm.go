//go:build !arm64 && !amd64

package par2

// No SIMD kernel for this architecture, the portable path handles everything.
const simdEnabled = false

func simdMulAdd(dst, src []byte, t *factorTables) int { return 0 }
