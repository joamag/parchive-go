package par2

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestDescribeAgreesWithCreate pins the exported Describe helper to what the
// encoder actually writes. Create no longer calls it, so without this the two
// descriptions of the same file could drift apart unnoticed.
func TestDescribeAgreesWithCreate(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(21))

	for _, size := range []int{1, 511, 512, 513, 5000, 20000} {
		buf := make([]byte, size)
		rng.Read(buf)
		path := filepath.Join(dir, "f.bin")
		if err := os.WriteFile(path, buf, 0o644); err != nil {
			t.Fatal(err)
		}

		want, err := Describe(path, 512)
		if err != nil {
			t.Fatal(err)
		}
		set, err := Create([]string{path}, 512, 0, 2, "test")
		if err != nil {
			t.Fatal(err)
		}
		got := set.Files[0]

		if got.ID != want.ID {
			t.Fatalf("size %d: file ID differs", size)
		}
		if got.MD5 != want.MD5 {
			t.Fatalf("size %d: whole-file hash differs", size)
		}
		if got.MD516k != want.MD516k {
			t.Fatalf("size %d: 16k hash differs", size)
		}
		if got.Size != want.Size || got.Name != want.Name {
			t.Fatalf("size %d: size or name differs", size)
		}
		if len(got.Slices) != len(want.Slices) {
			t.Fatalf("size %d: %d slices, want %d", size, len(got.Slices), len(want.Slices))
		}
		for i := range got.Slices {
			if got.Slices[i] != want.Slices[i] {
				t.Fatalf("size %d: slice %d checksums differ", size, i)
			}
		}
	}
}

// TestCreateBatchBoundaries walks sizes either side of the encoder's batch
// stride, where the padding and short-read handling live.
func TestCreateBatchBoundaries(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(22))
	const slice = 512

	for _, slices := range []int{1, 2, batchSlices - 1, batchSlices, batchSlices + 1, 2*batchSlices + 3} {
		for _, extra := range []int{0, 1, slice - 1} {
			size := slices*slice + extra
			buf := make([]byte, size)
			rng.Read(buf)
			path := filepath.Join(dir, "b.bin")
			if err := os.WriteFile(path, buf, 0o644); err != nil {
				t.Fatal(err)
			}

			set, err := Create([]string{path}, slice, 0, 3, "test")
			if err != nil {
				t.Fatalf("size %d: %v", size, err)
			}
			ref, err := Describe(path, slice)
			if err != nil {
				t.Fatal(err)
			}
			if len(set.Files[0].Slices) != len(ref.Slices) {
				t.Fatalf("size %d: slice count %d, want %d", size, len(set.Files[0].Slices), len(ref.Slices))
			}
			for i := range ref.Slices {
				if set.Files[0].Slices[i] != ref.Slices[i] {
					t.Fatalf("size %d: slice %d differs", size, i)
				}
			}
		}
	}
}

// TestCreateOrdersFilesByID checks the ordering rule that decides the global
// slice numbering, using enough files that the sort has real work to do.
func TestCreateOrdersFilesByID(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(23))

	var paths []string
	for i := 0; i < 8; i++ {
		buf := make([]byte, 700+i*137)
		rng.Read(buf)
		p := filepath.Join(dir, string(rune('a'+i))+".bin")
		if err := os.WriteFile(p, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}

	set, err := Create(paths, 256, 0, 4, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Files) != len(paths) {
		t.Fatalf("got %d files, want %d", len(set.Files), len(paths))
	}
	for i := 1; i < len(set.Files); i++ {
		if bytes.Compare(set.Files[i-1].ID[:], set.Files[i].ID[:]) >= 0 {
			t.Fatal("files are not sorted by ID")
		}
	}

	// And the whole set must still round trip through verify.
	status, err := set.Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range status {
		if !st.OK {
			t.Fatalf("%s did not verify against its own recovery set", st.File.Name)
		}
	}
}

// TestCreateZeroRecovery covers the index-only case, where no encoding runs.
func TestCreateZeroRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "z.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, 3000), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Create([]string{path}, 512, 0, 0, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Recovery) != 0 {
		t.Fatalf("got %d recovery slices, want none", len(set.Recovery))
	}
	if len(set.Files[0].Slices) != 6 {
		t.Fatalf("got %d slices, want 6", len(set.Files[0].Slices))
	}
}

func TestGFEdgeCases(t *testing.T) {
	if got := gfDiv(0, 5); got != 0 {
		t.Fatalf("gfDiv(0, 5) = %d, want 0", got)
	}
	if got := gfPow(0, 3); got != 0 {
		t.Fatalf("gfPow(0, 3) = %d, want 0", got)
	}
	if got := gfPow(5, 0); got != 1 {
		t.Fatalf("gfPow(5, 0) = %d, want 1", got)
	}
	if got := gfMul(0, 5); got != 0 {
		t.Fatalf("gfMul(0, 5) = %d, want 0", got)
	}
}
