package main

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// setup builds a small recovery set in a temp directory and returns the
// directory, the recovery file and the original contents of each input.
func setup(t *testing.T, sizes map[string]int, args ...string) (string, string, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(101))

	want := map[string][]byte{}
	var names []string
	for name, size := range sizes {
		buf := make([]byte, size)
		rng.Read(buf)
		if err := os.WriteFile(filepath.Join(dir, name), buf, 0o644); err != nil {
			t.Fatal(err)
		}
		want[name] = buf
		names = append(names, name)
	}
	sortStringSlice(names)

	archive := filepath.Join(dir, "set.par2")
	argv := append([]string{"parchive", "create", "-q", "-q"}, args...)
	argv = append(argv, archive)
	for _, n := range names {
		argv = append(argv, filepath.Join(dir, n))
	}
	if code := run(argv); code != exitSuccess {
		t.Fatalf("create exited with %d", code)
	}
	return dir, archive, want
}

func sortStringSlice(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestExitCodes pins the contract par2cmdline defines, which is what scripts
// wrapping this command depend on.
func TestExitCodes(t *testing.T) {
	t.Run("clean verify succeeds", func(t *testing.T) {
		_, archive, _ := setup(t, map[string]int{"a.bin": 20000}, "-s1024", "-c10")
		if code := run([]string{"parchive", "verify", "-q", "-q", archive}); code != exitSuccess {
			t.Fatalf("got %d, want %d", code, exitSuccess)
		}
	})

	t.Run("repairable damage reports repair possible", func(t *testing.T) {
		dir, archive, want := setup(t, map[string]int{"a.bin": 20000}, "-s1024", "-c10")
		damaged := append([]byte(nil), want["a.bin"]...)
		copy(damaged[500:], bytes.Repeat([]byte{0xAA}, 2000))
		if err := os.WriteFile(filepath.Join(dir, "a.bin"), damaged, 0o644); err != nil {
			t.Fatal(err)
		}
		if code := run([]string{"parchive", "verify", "-q", "-q", archive}); code != exitRepairPossible {
			t.Fatalf("got %d, want %d", code, exitRepairPossible)
		}
	})

	t.Run("unrepairable damage reports repair not possible", func(t *testing.T) {
		dir, archive, _ := setup(t, map[string]int{"a.bin": 20000}, "-s1024", "-c1")
		if err := os.WriteFile(filepath.Join(dir, "a.bin"), make([]byte, 20000), 0o644); err != nil {
			t.Fatal(err)
		}
		if code := run([]string{"parchive", "verify", "-q", "-q", archive}); code != exitRepairNotPossible {
			t.Fatalf("got %d, want %d", code, exitRepairNotPossible)
		}
	})

	t.Run("bad arguments report invalid command line", func(t *testing.T) {
		if code := run([]string{"parchive", "bogus", "x.par2"}); code != exitInvalidArgs {
			t.Fatalf("got %d, want %d", code, exitInvalidArgs)
		}
	})

	t.Run("missing recovery file reports insufficient data", func(t *testing.T) {
		dir := t.TempDir()
		if code := run([]string{"parchive", "verify", "-q", "-q", filepath.Join(dir, "nope.par2")}); code != exitInsufficientData {
			t.Fatalf("got %d, want %d", code, exitInsufficientData)
		}
	})
}

// TestRepairRoundTrip drives the whole command the way a user would.
func TestRepairRoundTrip(t *testing.T) {
	dir, archive, want := setup(t,
		map[string]int{"one.bin": 30000, "two.bin": 9000}, "-s1024", "-c20")

	// One file corrupted, the other deleted outright.
	damaged := append([]byte(nil), want["one.bin"]...)
	copy(damaged[1000:], bytes.Repeat([]byte{0x5A}, 3000))
	if err := os.WriteFile(filepath.Join(dir, "one.bin"), damaged, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "two.bin")); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"parchive", "repair", "-q", "-q", archive}); code != exitSuccess {
		t.Fatalf("repair exited with %d", code)
	}
	for name, w := range want {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, w) {
			t.Fatalf("%s was not restored", name)
		}
	}
}

// A byte inserted at the front moves every block, and must cost no recovery
// data to put right.
func TestRepairShiftedFile(t *testing.T) {
	dir, archive, want := setup(t, map[string]int{"a.bin": 20000}, "-s1024", "-c10")
	shifted := append([]byte{'X'}, want["a.bin"]...)
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), shifted, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"parchive", "repair", "-q", "-q", archive}); code != exitSuccess {
		t.Fatalf("repair exited with %d", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want["a.bin"]) {
		t.Fatal("shifted file was not restored")
	}
}

// TestPurgeRemovesRecoveryFiles covers -p.
func TestPurgeRemovesRecoveryFiles(t *testing.T) {
	dir, archive, _ := setup(t, map[string]int{"a.bin": 20000}, "-s1024", "-c10")
	if code := run([]string{"parchive", "repair", "-q", "-q", "-p", archive}); code != exitSuccess {
		t.Fatalf("repair exited with %d", code)
	}
	left, err := filepath.Glob(filepath.Join(dir, "*.par2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("recovery files were left behind: %v", left)
	}
}

// TestPar1RoundTrip covers the PAR1 path, which par2cmdline can only repair.
func TestPar1RoundTrip(t *testing.T) {
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(202))
	want := make([]byte, 12000)
	rng.Read(want)
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), want, 0o644); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(dir, "set.par")
	if code := run([]string{"parchive", "create", "-q", "-q", "-c2", archive, filepath.Join(dir, "a.bin")}); code != exitSuccess {
		t.Fatalf("create exited with %d", code)
	}
	if err := os.Remove(filepath.Join(dir, "a.bin")); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"parchive", "repair", "-q", "-q", archive}); code != exitSuccess {
		t.Fatalf("repair exited with %d", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("PAR1 file was not restored")
	}
}
