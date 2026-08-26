package par1

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// GF(2^8)
// ---------------------------------------------------------------------------

func TestGFFieldAxioms(t *testing.T) {
	for a := 1; a < 256; a++ {
		v := byte(a)
		if got := gfMul(v, 1); got != v {
			t.Fatalf("gfMul(%d, 1) = %d, want %d", v, got, v)
		}
		if got := gfMul(v, 0); got != 0 {
			t.Fatalf("gfMul(%d, 0) = %d, want 0", v, got)
		}
		if got := gfMul(v, gfDiv(1, v)); got != 1 {
			t.Fatalf("%d * %d^-1 = %d, want 1", v, v, got)
		}
	}
}

func TestGFPowMatchesRepeatedMultiplication(t *testing.T) {
	for _, base := range []byte{2, 3, 17, 255} {
		acc := byte(1)
		for n := 0; n < 40; n++ {
			if got := gfPow(base, n); got != acc {
				t.Fatalf("gfPow(%d, %d) = %d, want %d", base, n, got, acc)
			}
			acc = gfMul(acc, base)
		}
	}
}

// TestGFMatchesPAR1Polynomial pins the field to the one PAR1 actually uses.
// The exponent table of 0x11D is what makes our volumes readable by par and
// gopar, so a change here is a compatibility break, not a refactor.
func TestGFMatchesPAR1Polynomial(t *testing.T) {
	want := []byte{1, 2, 4, 8, 16, 32, 64, 128, 29, 58, 116, 232, 205, 135, 19, 38}
	for i, w := range want {
		if gfExp[i] != w {
			t.Fatalf("gfExp[%d] = %d, want %d (polynomial is not 0x11D)", i, gfExp[i], w)
		}
	}
}

func TestMulAddIsItsOwnInverse(t *testing.T) {
	src := make([]byte, 512)
	rand.New(rand.NewSource(3)).Read(src)
	dst := make([]byte, 512)
	want := append([]byte(nil), dst...)

	mulAdd(dst, src, 0x8D)
	if bytes.Equal(dst, want) {
		t.Fatal("mulAdd had no effect")
	}
	mulAdd(dst, src, 0x8D)
	if !bytes.Equal(dst, want) {
		t.Fatal("applying mulAdd twice did not cancel out")
	}
}

// ---------------------------------------------------------------------------
// Header and entries
// ---------------------------------------------------------------------------

// buildSet writes files into a temp dir and produces an index plus count volumes.
func buildSet(t *testing.T, files []struct {
	name string
	size int
}, count int) (dir, index string) {
	t.Helper()
	dir = t.TempDir()
	rng := rand.New(rand.NewSource(11))

	var paths []string
	for _, f := range files {
		buf := make([]byte, f.size)
		rng.Read(buf)
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	set, err := Create(paths, count, 0)
	if err != nil {
		t.Fatal(err)
	}
	index = filepath.Join(dir, "set.par")
	idx, err := os.Create(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.WriteIndex(idx); err != nil {
		t.Fatal(err)
	}
	_ = idx.Close()

	for v := 1; v <= count; v++ {
		vf, err := os.Create(filepath.Join(dir, "set.p0"+string(rune('0'+v))))
		if err != nil {
			t.Fatal(err)
		}
		if err := set.WriteVolume(vf, uint64(v)); err != nil {
			t.Fatal(err)
		}
		_ = vf.Close()
	}
	return dir, index
}

func parseSet(t *testing.T, dir string) *Set {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "set.*"))
	if err != nil {
		t.Fatal(err)
	}
	set, err := Parse(paths...)
	if err != nil {
		t.Fatal(err)
	}
	return set
}

var twoFiles = []struct {
	name string
	size int
}{{"a.bin", 5000}, {"b.bin", 811}}

func TestHeaderLayout(t *testing.T) {
	dir, index := buildSet(t, twoFiles, 3)
	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw[:8], magic[:]) {
		t.Fatalf("magic = %x, want %x", raw[:8], magic)
	}
	if got := binary.LittleEndian.Uint32(raw[0x08:]); got != Version {
		t.Fatalf("version = %#x, want %#x", got, Version)
	}
	if got := binary.LittleEndian.Uint64(raw[0x30:]); got != 0 {
		t.Fatalf("index volume number = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint64(raw[0x38:]); got != 2 {
		t.Fatalf("file count = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint64(raw[0x40:]); got != headerSize {
		t.Fatalf("file list offset = %d, want %d", got, headerSize)
	}
	// The control hash covers everything from the set hash onwards.
	sum := md5.Sum(raw[0x20:])
	if !bytes.Equal(sum[:], raw[0x10:0x20]) {
		t.Fatal("control hash does not cover 0x20..end")
	}
	// And the set hash is the MD5 of the concatenated file hashes.
	set := parseSet(t, dir)
	if set.setHash() != set.SetHash {
		t.Fatal("set hash is not the MD5 of the file hashes")
	}
}

func TestUnicodeFileNamesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	// PAR1 stores names as UTF-16, so non-ASCII names must survive intact.
	names := []string{"ficheiro-português.bin", "日本語.bin", "emoji-🛟.bin"}
	var paths []string
	for i, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, bytes.Repeat([]byte{byte(i + 1)}, 300), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	set, err := Create(paths, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := set.WriteIndex(&buf); err != nil {
		t.Fatal(err)
	}
	_, _, files, _, err := parseVolume(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(names) {
		t.Fatalf("got %d entries, want %d", len(files), len(names))
	}
	for i, f := range files {
		if f.Name != names[i] {
			t.Fatalf("name %d = %q, want %q", i, f.Name, names[i])
		}
	}
}

func TestParseRejectsCorruptHeader(t *testing.T) {
	dir, index := buildSet(t, twoFiles, 2)
	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	raw[0x40] ^= 0xFF // break the control hash
	broken := filepath.Join(dir, "broken.par")
	if err := os.WriteFile(broken, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(broken); err == nil {
		t.Fatal("expected a corrupt volume to be rejected")
	}
}

// ---------------------------------------------------------------------------
// End-to-end
// ---------------------------------------------------------------------------

func TestCreateVerifyClean(t *testing.T) {
	dir, _ := buildSet(t, twoFiles, 3)
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

func TestRepairMissingFile(t *testing.T) {
	dir, _ := buildSet(t, twoFiles, 3)
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

func TestRepairTwoFilesAtOnce(t *testing.T) {
	dir, _ := buildSet(t, twoFiles, 3)
	want := map[string][]byte{}
	for _, n := range []string{"a.bin", "b.bin"} {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		want[n] = data
	}
	// One file corrupted in place, the other deleted outright.
	damaged := append([]byte(nil), want["a.bin"]...)
	copy(damaged[1000:], bytes.Repeat([]byte{0xAA}, 200))
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), damaged, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "b.bin")); err != nil {
		t.Fatal(err)
	}

	if err := parseSet(t, dir).Repair(dir); err != nil {
		t.Fatal(err)
	}
	for n, w := range want {
		got, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, w) {
			t.Fatalf("%s was not restored correctly", n)
		}
	}
}

func TestRepairFailsWithoutEnoughVolumes(t *testing.T) {
	dir, _ := buildSet(t, twoFiles, 1)
	if err := os.Remove(filepath.Join(dir, "a.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "b.bin")); err != nil {
		t.Fatal(err)
	}
	if err := parseSet(t, dir).Repair(dir); err == nil {
		t.Fatal("expected repair to fail with one volume and two lost files")
	}
}

func TestCreateRejectsEmptyInput(t *testing.T) {
	if _, err := Create(nil, 1, 0); err == nil {
		t.Fatal("expected an error for an empty file list")
	}
}

func TestVolumesHoldLargestFileSize(t *testing.T) {
	dir, _ := buildSet(t, twoFiles, 2)
	set := parseSet(t, dir)
	if set.VolumeSize != 5000 {
		t.Fatalf("volume size = %d, want 5000 (the largest file)", set.VolumeSize)
	}
}

func BenchmarkCreate(b *testing.B) {
	dir := b.TempDir()
	buf := make([]byte, 4<<20)
	rand.New(rand.NewSource(5)).Read(buf)
	path := filepath.Join(dir, "bench.bin")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Create([]string{path}, 10, 0); err != nil {
			b.Fatal(err)
		}
	}
}
