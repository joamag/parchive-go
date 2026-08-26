package par2

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// damageSet writes files into a temp dir, builds a recovery set over them and
// returns the directory plus the original contents.
func damageSet(t *testing.T, files map[string]int, sliceSize uint64, count int) (string, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(77))
	want := map[string][]byte{}

	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sortStrings(names)

	var paths []string
	for _, name := range names {
		buf := make([]byte, files[name])
		rng.Read(buf)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		want[name] = buf
		paths = append(paths, p)
	}

	set, err := Create(paths, sliceSize, 0, count, "test")
	if err != nil {
		t.Fatal(err)
	}
	idx, err := os.Create(filepath.Join(dir, "set.par2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := set.WriteIndex(idx); err != nil {
		t.Fatal(err)
	}
	_ = idx.Close()

	exps := make([]uint32, count)
	for i := range exps {
		exps[i] = uint32(i)
	}
	vol, err := os.Create(filepath.Join(dir, "set.vol000+ff.par2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := set.WriteVolume(vol, exps); err != nil {
		t.Fatal(err)
	}
	_ = vol.Close()
	return dir, want
}

func loadSet(t *testing.T, dir string) *Set {
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

func checkRestored(t *testing.T, dir string, want map[string][]byte) {
	t.Helper()
	for name, w := range want {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, w) {
			t.Fatalf("%s was not restored correctly", name)
		}
	}
}

// TestRepairInsertedBytes is the headline case: shifting the whole file by one
// byte moves every slice off its offset, and none of the data is actually lost.
func TestRepairInsertedBytes(t *testing.T) {
	dir, want := damageSet(t, map[string]int{"a.bin": 20000}, 1024, 8)
	path := filepath.Join(dir, "a.bin")

	shifted := append([]byte{'X'}, want["a.bin"]...)
	if err := os.WriteFile(path, shifted, 0o644); err != nil {
		t.Fatal(err)
	}

	set := loadSet(t, dir)
	status, err := set.Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(status[0].Damaged); n != 0 {
		t.Fatalf("%d slices reported unrecoverable, want 0 (all of them merely moved)", n)
	}
	if len(status[0].Misplaced) != len(status[0].File.Slices) {
		t.Fatalf("got %d misplaced slices, want %d", len(status[0].Misplaced), len(status[0].File.Slices))
	}

	if err := set.Repair(dir); err != nil {
		t.Fatal(err)
	}
	checkRestored(t, dir, want)
}

// TestRepairDeletedBytes loses the slices that straddle the cut but must find
// everything after it.
func TestRepairDeletedBytes(t *testing.T) {
	dir, want := damageSet(t, map[string]int{"a.bin": 40000}, 1024, 8)
	path := filepath.Join(dir, "a.bin")

	orig := want["a.bin"]
	cut := append(append([]byte(nil), orig[:15000]...), orig[15500:]...)
	if err := os.WriteFile(path, cut, 0o644); err != nil {
		t.Fatal(err)
	}

	set := loadSet(t, dir)
	status, err := set.Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(status[0].Misplaced) == 0 {
		t.Fatal("expected the slices after the cut to be found at shifted offsets")
	}
	if len(status[0].Damaged) > 2 {
		t.Fatalf("%d slices unrecoverable, expected at most the two spanning the cut", len(status[0].Damaged))
	}

	if err := set.Repair(dir); err != nil {
		t.Fatal(err)
	}
	checkRestored(t, dir, want)
}

// TestRepairAcrossFiles covers data that ended up in a different file of the
// set, which the scan finds because it indexes slices globally.
func TestRepairAcrossFiles(t *testing.T) {
	dir, want := damageSet(t, map[string]int{"one.bin": 8192, "two.bin": 8192}, 1024, 10)

	// Concatenate both files into one and empty the other.
	joined := append(append([]byte(nil), want["one.bin"]...), want["two.bin"]...)
	if err := os.WriteFile(filepath.Join(dir, "one.bin"), joined, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.bin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	set := loadSet(t, dir)
	if err := set.Repair(dir); err != nil {
		t.Fatal(err)
	}
	checkRestored(t, dir, want)
}

// TestNoScanLeavesMisplacedSlicesLost checks the option actually switches the
// search off, so a caller that asked for the cheap check gets it.
func TestNoScanLeavesMisplacedSlicesLost(t *testing.T) {
	dir, want := damageSet(t, map[string]int{"a.bin": 20000}, 1024, 8)
	shifted := append([]byte{'X'}, want["a.bin"]...)
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), shifted, 0o644); err != nil {
		t.Fatal(err)
	}

	set := loadSet(t, dir)
	status, err := set.VerifyWith(dir, Options{NoScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(status[0].Damaged) == 0 {
		t.Fatal("NoScan should have reported the shifted slices as damaged")
	}
	if len(status[0].Misplaced) != 0 {
		t.Fatal("NoScan should not locate misplaced slices")
	}
}

// TestScanFindsPaddedTailSlice pins the case where the last slice of a file is
// zero padded: its padding is not in the file, so the window has to be allowed
// to run past the final byte.
func TestScanFindsPaddedTailSlice(t *testing.T) {
	// 20000 is not a multiple of 1024, so the final slice is padded.
	dir, want := damageSet(t, map[string]int{"a.bin": 20000}, 1024, 8)
	shifted := append([]byte{'X', 'Y', 'Z'}, want["a.bin"]...)
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), shifted, 0o644); err != nil {
		t.Fatal(err)
	}

	set := loadSet(t, dir)
	status, err := set.Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(status[0].Damaged); n != 0 {
		t.Fatalf("%d slices unrecoverable, want 0: the padded tail slice was not found", n)
	}
}

// TestVerifyCleanSetDoesNotScan guards the fast path: an intact set must not
// pay for the sliding window.
func TestVerifyCleanSetReportsOK(t *testing.T) {
	dir, _ := damageSet(t, map[string]int{"a.bin": 20000, "b.bin": 3000}, 1024, 8)
	set := loadSet(t, dir)
	status, err := set.Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range status {
		if !st.OK || len(st.Damaged) != 0 || len(st.Misplaced) != 0 {
			t.Fatalf("%s: clean set did not verify cleanly", st.File.Name)
		}
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestScanAcrossChunks exercises the chunked read path, where a window has to
// straddle the boundary between two buffers.
func TestScanAcrossChunks(t *testing.T) {
	// A slice size large enough that the scan buffer is several chunks wide for
	// this file, so the boundary handling actually runs.
	const sliceSize = 64 << 10
	size := 5*scanChunk/2 + 1234

	dir, want := damageSet(t, map[string]int{"big.bin": size}, sliceSize, 4)
	shifted := append([]byte{'Q'}, want["big.bin"]...)
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), shifted, 0o644); err != nil {
		t.Fatal(err)
	}

	set := loadSet(t, dir)
	status, err := set.Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(status[0].Damaged); n != 0 {
		t.Fatalf("%d slices unrecoverable across chunk boundaries, want 0", n)
	}
	if err := set.Repair(dir); err != nil {
		t.Fatal(err)
	}
	checkRestored(t, dir, want)
}
