package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The expectations below were taken from par2cmdline 1.3.0 by running the same
// command and listing the files it produced. They are what makes this command
// interchangeable with it, so a change that breaks them is a compatibility
// break rather than a refactor.
func TestAllocateMatchesPar2cmdline(t *testing.T) {
	cases := []struct {
		name    string
		first   uint32
		blocks  uint32
		files   uint32
		sch     scheme
		largest uint32
		want    string
	}{
		{
			name:   "twenty blocks, default scheme",
			blocks: 20, files: 5, sch: schemeVariable,
			want: "s.vol00+1.par2 s.vol01+2.par2 s.vol03+4.par2 s.vol07+8.par2 s.vol15+5.par2",
		},
		{
			name:   "twenty blocks over three files is uniform",
			blocks: 20, files: 3, sch: schemeUniform,
			want: "s.vol00+7.par2 s.vol07+7.par2 s.vol14+6.par2",
		},
		{
			name:   "single block",
			blocks: 1, files: 1, sch: schemeVariable,
			want: "s.vol0+1.par2",
		},
		{
			name:  "first block offset shifts every exponent",
			first: 5, blocks: 20, files: 5, sch: schemeVariable,
			want: "s.vol05+1.par2 s.vol06+2.par2 s.vol08+4.par2 s.vol12+8.par2 s.vol20+5.par2",
		},
		{
			name:   "one hundred blocks widen the count field",
			blocks: 100, files: 7, sch: schemeVariable,
			want: "s.vol000+01.par2 s.vol001+02.par2 s.vol003+04.par2 s.vol007+08.par2 " +
				"s.vol015+16.par2 s.vol031+32.par2 s.vol063+37.par2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := allocate("s", tc.first, tc.blocks, tc.files, tc.sch, tc.largest)
			var names []string
			for _, a := range got {
				names = append(names, a.name)
			}
			if strings.Join(names, " ") != tc.want {
				t.Fatalf("got %q\nwant %q", strings.Join(names, " "), tc.want)
			}

			// Every exponent must appear exactly once across the files.
			total := uint32(0)
			next := tc.first
			for _, a := range got {
				if a.exponent != next {
					t.Fatalf("file %q starts at %d, expected %d", a.name, a.exponent, next)
				}
				next += a.count
				total += a.count
			}
			if total != tc.blocks {
				t.Fatalf("files hold %d blocks, want %d", total, tc.blocks)
			}
		})
	}
}

func TestRecoveryFileCount(t *testing.T) {
	// Roughly log2, which is what keeps the file count sane for large sets.
	for _, tc := range []struct{ blocks, want uint32 }{
		{1, 1}, {2, 2}, {3, 2}, {4, 3}, {7, 3}, {8, 4}, {20, 5}, {100, 7}, {1000, 10},
	} {
		if got := recoveryFileCount(tc.blocks); got != tc.want {
			t.Fatalf("recoveryFileCount(%d) = %d, want %d", tc.blocks, got, tc.want)
		}
	}
}

func TestRecoveryBlocksFor(t *testing.T) {
	// Rounds to nearest, and never returns zero for a non-zero redundancy.
	for _, tc := range []struct{ blocks, pct, want uint32 }{
		{100, 5, 5}, {37, 5, 2}, {10, 5, 1}, {1, 5, 1}, {100, 100, 100}, {200, 10, 20},
	} {
		if got := recoveryBlocksFor(tc.blocks, tc.pct); got != tc.want {
			t.Fatalf("recoveryBlocksFor(%d, %d%%) = %d, want %d", tc.blocks, tc.pct, got, tc.want)
		}
	}
}

func TestBlockSizeFor(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := write("a.bin", 300000)

	// The derived size must produce no more blocks than were asked for, and be
	// a multiple of four as the format requires.
	for _, count := range []uint32{1, 2, 10, 100, 2000} {
		size, err := blockSizeFor([]string{a}, count)
		if err != nil {
			t.Fatalf("count %d: %v", count, err)
		}
		if size == 0 || size%4 != 0 {
			t.Fatalf("count %d: block size %d is not a positive multiple of four", count, size)
		}
		blocks := (uint64(300000) + size - 1) / size
		if blocks > uint64(count) {
			t.Fatalf("count %d: block size %d yields %d blocks", count, size, blocks)
		}
	}

	b := write("b.bin", 90000)
	if _, err := blockSizeFor([]string{a, b}, 1); err == nil {
		t.Fatal("a block count below the file count should be rejected")
	}
}
