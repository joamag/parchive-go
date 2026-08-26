package par2

import (
	"bytes"
	"crypto/md5"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// GF(2^16)
// ---------------------------------------------------------------------------

func TestGFFieldAxioms(t *testing.T) {
	for _, a := range []uint16{1, 2, 3, 0x1234, 0xABCD, 0xFFFF} {
		if got := gfMul(a, 1); got != a {
			t.Fatalf("gfMul(%d, 1) = %d, want %d", a, got, a)
		}
		if got := gfMul(a, 0); got != 0 {
			t.Fatalf("gfMul(%d, 0) = %d, want 0", a, got)
		}
		if got := gfMul(a, gfDiv(1, a)); got != 1 {
			t.Fatalf("a * a^-1 = %d, want 1 (a=%d)", got, a)
		}
	}
	for _, tc := range [][3]uint16{{2, 3, 5}, {0x1234, 0xABCD, 0x7F}} {
		a, b, c := tc[0], tc[1], tc[2]
		if gfMul(gfMul(a, b), c) != gfMul(a, gfMul(b, c)) {
			t.Fatalf("multiplication is not associative for %v", tc)
		}
		if gfMul(a, b) != gfMul(b, a) {
			t.Fatalf("multiplication is not commutative for %v", tc)
		}
	}
}

func TestGFPowMatchesRepeatedMultiplication(t *testing.T) {
	for _, base := range []uint16{2, 3, 0x1234} {
		acc := uint16(1)
		for n := uint32(0); n < 40; n++ {
			if got := gfPow(base, n); got != acc {
				t.Fatalf("gfPow(%d, %d) = %d, want %d", base, n, got, acc)
			}
			acc = gfMul(acc, base)
		}
	}
}

func TestInputConstantsAreDistinctAndFullOrder(t *testing.T) {
	consts, err := inputConstants(2000)
	if err != nil {
		t.Fatal(err)
	}
	if len(consts) != 2000 {
		t.Fatalf("got %d constants, want 2000", len(consts))
	}
	seen := map[uint16]bool{}
	for i, c := range consts {
		if c == 0 || c == 1 {
			t.Fatalf("constant %d at index %d is degenerate", c, i)
		}
		if seen[c] {
			t.Fatalf("constant %d repeats at index %d", c, i)
		}
		seen[c] = true
	}
	if _, err := inputConstants(MaxInputSlices + 1); err == nil {
		t.Fatal("expected an error past MaxInputSlices")
	}
}

// ---------------------------------------------------------------------------
// Packets
// ---------------------------------------------------------------------------

func TestPacketRoundTrip(t *testing.T) {
	p := Packet{SetID: [16]byte{1, 2, 3}, Type: TypeCreator, Body: []byte("par2go")}
	got := ReadPackets(p.Bytes())
	if len(got) != 1 {
		t.Fatalf("read %d packets, want 1", len(got))
	}
	if got[0].SetID != p.SetID || got[0].Type != p.Type {
		t.Fatal("packet header did not survive the round trip")
	}
	if !bytes.HasPrefix(got[0].Body, p.Body) {
		t.Fatalf("body = %q, want prefix %q", got[0].Body, p.Body)
	}
}

func TestReadPacketsSkipsCorruption(t *testing.T) {
	good := Packet{SetID: [16]byte{9}, Type: TypeCreator, Body: []byte("intact")}.Bytes()
	bad := Packet{SetID: [16]byte{9}, Type: TypeCreator, Body: []byte("broken")}.Bytes()
	bad[70] ^= 0xFF // flip a body byte so the MD5 no longer matches

	stream := append(append([]byte("junk before"), bad...), good...)
	got := ReadPackets(stream)
	if len(got) != 1 {
		t.Fatalf("read %d packets, want only the intact one", len(got))
	}
	if !bytes.HasPrefix(got[0].Body, []byte("intact")) {
		t.Fatalf("recovered the wrong packet: %q", got[0].Body)
	}
}

// ---------------------------------------------------------------------------
// End-to-end
// ---------------------------------------------------------------------------

// buildSet writes the given files into a temp dir and produces an index plus a
// single recovery volume holding count slices.
func buildSet(t *testing.T, files map[string]int, sliceSize uint64, count int) (dir, index string) {
	t.Helper()
	dir = t.TempDir()
	rng := rand.New(rand.NewSource(42))

	var paths []string
	for name, size := range files {
		buf := make([]byte, size)
		rng.Read(buf)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	set, err := Create(paths, sliceSize, 0, count, "par2go test")
	if err != nil {
		t.Fatal(err)
	}
	index = filepath.Join(dir, "set.par2")
	idx, err := os.Create(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.WriteIndex(idx); err != nil {
		t.Fatal(err)
	}
	idx.Close()

	exps := make([]uint32, count)
	for i := range exps {
		exps[i] = uint32(i)
	}
	vf, err := os.Create(filepath.Join(dir, "set.vol000+ff.par2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := set.WriteVolume(vf, exps); err != nil {
		t.Fatal(err)
	}
	vf.Close()
	return dir, index
}

func parseSet(t *testing.T, dir string) *Set {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "set*.par2"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := Parse(paths...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func TestCreateVerifyClean(t *testing.T) {
	dir, _ := buildSet(t, map[string]int{"a.bin": 5000, "b.bin": 811}, 512, 10)
	status, err := parseSet(t, dir).Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 2 {
		t.Fatalf("got %d files, want 2", len(status))
	}
	for _, st := range status {
		if !st.OK {
			t.Fatalf("%s reported as damaged on a pristine set", st.File.Name)
		}
	}
}

func TestRepairDamagedSlices(t *testing.T) {
	dir, _ := buildSet(t, map[string]int{"a.bin": 5000, "b.bin": 811}, 512, 10)
	target := filepath.Join(dir, "a.bin")
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	damaged := append([]byte(nil), original...)
	for _, off := range []int{0, 1500, 4800} {
		copy(damaged[off:], bytes.Repeat([]byte{0xAA}, 40))
	}
	if err := os.WriteFile(target, damaged, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := parseSet(t, dir).Repair(dir); err != nil {
		t.Fatal(err)
	}
	repaired, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if md5.Sum(repaired) != md5.Sum(original) {
		t.Fatal("repaired file does not match the original")
	}
}

func TestRepairMissingFile(t *testing.T) {
	dir, _ := buildSet(t, map[string]int{"a.bin": 5000, "b.bin": 811}, 512, 10)
	target := filepath.Join(dir, "b.bin")
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}

	if err := parseSet(t, dir).Repair(dir); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("file was not recreated: %v", err)
	}
	if !bytes.Equal(restored, original) {
		t.Fatal("reconstructed file differs from the original")
	}
}

func TestRepairFailsWithoutEnoughRecoverySlices(t *testing.T) {
	dir, _ := buildSet(t, map[string]int{"a.bin": 4096}, 512, 2)
	target := filepath.Join(dir, "a.bin")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range []int{0, 600, 1200, 1800} { // 4 slices, only 2 recovery
		copy(data[off:], bytes.Repeat([]byte{0x00}, 64))
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := parseSet(t, dir).Repair(dir); err == nil {
		t.Fatal("expected repair to fail when recovery slices are exhausted")
	}
}

func TestVerifySurvivesDamagedIndex(t *testing.T) {
	dir, index := buildSet(t, map[string]int{"a.bin": 3000}, 512, 8)
	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	for i := 100; i < len(raw) && i < 260; i++ {
		raw[i] ^= 0xFF
	}
	if err := os.WriteFile(index, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// The volume repeats every critical packet, so the set is still readable.
	status, err := parseSet(t, dir).Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 || !status[0].OK {
		t.Fatal("set should still verify from the volume's duplicated critical packets")
	}
}

func TestCreateRejectsBadSliceSize(t *testing.T) {
	for _, size := range []uint64{0, 3, 1023} {
		if _, err := Create(nil, size, 0, 1, ""); err == nil {
			t.Fatalf("slice size %d should have been rejected", size)
		}
	}
}

func TestParseWithoutMainPacketFails(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.par2")
	if err := os.WriteFile(p, []byte("not a par2 file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(p); err == nil {
		t.Fatal("expected an error when no main packet is present")
	}
}

func TestFileIDIsStable(t *testing.T) {
	var h [16]byte
	copy(h[:], "0123456789abcdef")
	a := FileID(h, 1234, "movie.mkv")
	b := FileID(h, 1234, "movie.mkv")
	if a != b {
		t.Fatal("FileID is not deterministic")
	}
	if FileID(h, 1234, "other.mkv") == a {
		t.Fatal("FileID ignores the file name")
	}
	if FileID(h, 9999, "movie.mkv") == a {
		t.Fatal("FileID ignores the file size")
	}
}

func BenchmarkCreate(b *testing.B) {
	dir := b.TempDir()
	buf := make([]byte, 64<<20)
	rand.New(rand.NewSource(7)).Read(buf)
	path := filepath.Join(dir, "bench.bin")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Create([]string{path}, 512<<10, 0, 20, "bench"); err != nil {
			b.Fatal(err)
		}
	}
}
