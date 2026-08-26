package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joamag/parchive-go/par1"
	"github.com/joamag/parchive-go/par2"
)

func creator() string { return "parchive-go version " + version }

func create(c *config) int {
	if strings.EqualFold(filepath.Ext(c.archive), ".par") {
		return createPar1(c)
	}
	return createPar2(c)
}

func createPar2(c *config) int {
	blockSize := c.blockSize
	if !c.blockSizeSet {
		size, err := blockSizeFor(c.files, c.blockCount)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitInvalidArgs
		}
		blockSize = size
	}

	var sourceBlocks uint32
	var largest uint64
	for _, f := range c.files {
		st, err := os.Stat(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitFileIOError
		}
		sourceBlocks += uint32((uint64(st.Size()) + blockSize - 1) / blockSize)
		if uint64(st.Size()) > largest {
			largest = uint64(st.Size())
		}
	}
	if sourceBlocks > 32768 {
		fmt.Fprintf(os.Stderr, "Too many source blocks (%d > 32768).\n", sourceBlocks)
		return exitInvalidArgs
	}

	recoveryBlocks := c.recoveryBlocks
	switch {
	case c.recoveryBlocksSet:
	case c.redundancySize > 0:
		recoveryBlocks = recoveryBlocksForSize(sourceBlocks, blockSize, c.redundancySize, c.recoveryFiles)
	default:
		recoveryBlocks = recoveryBlocksFor(sourceBlocks, c.redundancy)
	}
	if recoveryBlocks > 65536 {
		fmt.Fprintln(os.Stderr, "Too many recovery blocks requested.")
		return exitInvalidArgs
	}
	if uint64(c.firstBlock)+uint64(recoveryBlocks) >= 65536 {
		fmt.Fprintln(os.Stderr, "First recovery block number is too high.")
		return exitInvalidArgs
	}

	files := c.recoveryFiles
	if files == 0 {
		files = recoveryFileCount(recoveryBlocks)
	}
	if files > recoveryBlocks {
		files = recoveryBlocks
	}

	base := strings.TrimSuffix(c.archive, filepath.Ext(c.archive))
	largestBlocks := uint32((largest + blockSize - 1) / blockSize)
	allocs := allocate(base, c.firstBlock, recoveryBlocks, files, c.scheme, largestBlocks)

	c.out(noiseNormal, "Block size: %d\nSource file count: %d\nSource block count: %d\n"+
		"Recovery block count: %d\nRecovery file count: %d\n\n",
		blockSize, len(c.files), sourceBlocks, recoveryBlocks, len(allocs))

	for _, f := range c.files {
		c.out(noiseNormal, "Opening: %s\n", f)
	}

	set, err := par2.Create(c.files, blockSize, c.firstBlock, int(recoveryBlocks), creator())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}

	c.out(noiseNormal, "Computing Reed Solomon matrix.\nConstructing: done.\n")
	c.out(noiseNormal, "Writing recovery packets\n")

	written := uint64(0)
	for _, a := range allocs {
		exps := make([]uint32, a.count)
		for i := range exps {
			exps[i] = a.exponent + uint32(i)
		}
		f, err := os.Create(a.name)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitFileIOError
		}
		if err := set.WriteVolume(f, exps); err != nil {
			f.Close()
			fmt.Fprintln(os.Stderr, err)
			return exitFileIOError
		}
		if st, err := f.Stat(); err == nil {
			written += uint64(st.Size())
		}
		if err := f.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitFileIOError
		}
		c.out(noiseNoisy, "Wrote %s\n", a.name)
	}

	c.out(noiseNormal, "Writing verification packets\n")
	idx, err := os.Create(c.archive)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}
	if err := set.WriteIndex(idx); err != nil {
		idx.Close()
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}
	if err := idx.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}

	c.out(noiseNormal, "Done\n")
	return exitSuccess
}

// createPar1 writes a PAR1 set. par2cmdline cannot create these, so the options
// that only make sense for PAR2 are ignored here.
func createPar1(c *config) int {
	count := c.recoveryBlocks
	if !c.recoveryBlocksSet {
		count = uint32(len(c.files)) * c.redundancy / 100
		if count == 0 {
			count = 1
		}
	}

	set, err := par1.Create(c.files, int(count), 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInvalidArgs
	}

	c.out(noiseNormal, "Source file count: %d\nRecovery volume count: %d\nVolume size: %d\n\n",
		len(set.Files), count, set.VolumeSize)

	idx, err := os.Create(c.archive)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}
	if err := set.WriteIndex(idx); err != nil {
		idx.Close()
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}
	idx.Close()

	base := strings.TrimSuffix(c.archive, filepath.Ext(c.archive))
	for v := uint32(1); v <= count; v++ {
		name := fmt.Sprintf("%s.p%02d", base, v)
		f, err := os.Create(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitFileIOError
		}
		if err := set.WriteVolume(f, uint64(v)); err != nil {
			f.Close()
			fmt.Fprintln(os.Stderr, err)
			return exitFileIOError
		}
		if err := f.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitFileIOError
		}
		c.out(noiseNoisy, "Wrote %s\n", name)
	}
	c.out(noiseNormal, "Done\n")
	return exitSuccess
}
