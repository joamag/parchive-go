package main

import (
	"fmt"
	"os"
)

// The sizing and naming rules below mirror par2cmdline exactly, because they
// decide what a recovery set looks like on disk. Getting them wrong would not
// corrupt anything, but it would mean the same command produced a different set
// of files here than it does there.

// scheme selects how recovery blocks are spread across recovery files.
type scheme int

const (
	schemeVariable scheme = iota // exponentially sized files, the default
	schemeUniform                // every file the same size, -u
	schemeLimited                // exponential but capped at the largest file, -l
)

// blockSizeFor derives a block size that yields about blockCount blocks across
// the given files. par2cmdline works in units of four bytes throughout, which
// is why the arithmetic below divides and multiplies by four rather than
// rounding at the end.
func blockSizeFor(files []string, blockCount uint32) (uint64, error) {
	if uint64(blockCount) < uint64(len(files)) {
		// This message, and the two below, reproduce par2cmdline's wording
		// verbatim so that a script reading stderr sees what it expects. That
		// is why they break the usual Go convention on error strings.
		//nolint:staticcheck // ST1005: matches par2cmdline output
		return 0, fmt.Errorf("Block count (%d) cannot be smaller than the number of files(%d). ",
			blockCount, len(files))
	}

	sizes := make([]uint64, len(files))
	var largest, quarters uint64
	for i, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			return 0, err
		}
		sizes[i] = uint64(st.Size())
		if sizes[i] > largest {
			largest = sizes[i]
		}
		quarters += (sizes[i] + 3) / 4
	}

	if uint64(blockCount) == uint64(len(files)) {
		return (largest + 3) &^ 3, nil
	}
	if uint64(blockCount) > quarters {
		return 4, nil
	}

	countFor := func(size uint64) uint64 {
		var n uint64
		for _, s := range sizes {
			n += ((s+3)/4 + size - 1) / size
		}
		return n
	}

	lower := quarters / uint64(blockCount)
	upper := (quarters + uint64(blockCount) - uint64(len(files)) - 1) / (uint64(blockCount) - uint64(len(files)))
	var size, count uint64
	for lower < upper {
		size = (lower + upper) / 2
		count = countFor(size)
		if count > uint64(blockCount) {
			lower = size + 1
			if lower >= upper {
				size = lower
				count = countFor(size)
			}
		} else {
			upper = size
		}
	}
	if count > 32768 {
		//nolint:staticcheck // ST1005: matches par2cmdline output
		return 0, fmt.Errorf("Error calculating block size. cannot be higher than 32768.")
	}
	if count == 0 {
		//nolint:staticcheck // ST1005: matches par2cmdline output
		return 0, fmt.Errorf("Error calculating block size. cannot be 0.")
	}
	return size * 4, nil
}

// recoveryBlocksFor turns a redundancy percentage into a block count, rounding
// to nearest and never returning zero.
func recoveryBlocksFor(sourceBlocks, redundancy uint32) uint32 {
	n := (sourceBlocks*redundancy + 50) / 100
	if n == 0 && redundancy > 0 {
		n = 1
	}
	return n
}

// recoveryBlocksForSize turns a redundancy target size into a block count,
// allowing for the critical packets each recovery file has to repeat.
func recoveryBlocksForSize(sourceBlocks uint32, blockSize, target uint64, files uint32) uint32 {
	perFile := uint64(sourceBlocks) * 21
	packet := blockSize + 70
	if files == 0 {
		overhead := 15 * perFile
		est := uint32(1)
		if overhead <= target {
			est = uint32((target - overhead) / packet)
		}
		files = recoveryFileCount(est)
	}
	overhead := uint64(files) * perFile
	if overhead > target {
		return 1
	}
	return uint32((target - overhead) / packet)
}

// recoveryFileCount is roughly log2 of the block count, which keeps the number
// of files sane for large sets and lets their sizes grow exponentially.
func recoveryFileCount(blocks uint32) uint32 {
	var n uint32
	for b := blocks; b > 0; b >>= 1 {
		n++
	}
	return n
}

// allocation is one recovery file: a run of exponents and how many it holds.
type allocation struct {
	name     string
	exponent uint32
	count    uint32
}

// allocate spreads recoveryBlocks across files according to the scheme, then
// names each file the way par2cmdline does.
func allocate(base string, first, recoveryBlocks, files uint32, s scheme, largestFileBlocks uint32) []allocation {
	if recoveryBlocks == 0 || files == 0 {
		return nil
	}
	out := make([]allocation, files)

	exponent := first
	switch s {
	case schemeUniform:
		per := recoveryBlocks / files
		rem := recoveryBlocks % files
		for i := uint32(0); i < files; i++ {
			n := per
			if i < rem {
				n++
			}
			out[i] = allocation{exponent: exponent, count: n}
			exponent += n
		}

	case schemeLimited:
		// Fill from the top with equal, capped files, then exponentially from
		// the bottom with whatever is left.
		largest := largestFileBlocks
		if largest == 0 {
			largest = 1
		}
		idx := files
		blocks := recoveryBlocks
		exponent = first + recoveryBlocks
		for blocks >= 2*largest && idx > 0 {
			idx--
			exponent -= largest
			blocks -= largest
			out[idx] = allocation{exponent: exponent, count: largest}
		}
		exponent = first
		count := uint32(1)
		for i := uint32(0); i < idx; i++ {
			n := count
			if blocks < n {
				n = blocks
			}
			out[i] = allocation{exponent: exponent, count: n}
			exponent += n
			blocks -= n
			count <<= 1
		}

	default: // schemeVariable
		low := uint32(1)
		max := uint32(1)<<files - 1
		for max < recoveryBlocks {
			low <<= 1
			max <<= 1
		}
		blocks := recoveryBlocks
		for i := uint32(0); i < files; i++ {
			n := low
			if blocks < n {
				n = blocks
			}
			out[i] = allocation{exponent: exponent, count: n}
			exponent += n
			blocks -= n
			low <<= 1
		}
	}

	// Digit widths take the trailing empty allocation into account, which is why
	// twenty blocks starting at zero give "vol00+1" and not "vol0+1".
	limitLow, limitCount := exponent, uint32(0)
	for _, a := range out {
		if a.exponent > limitLow {
			limitLow = a.exponent
		}
		if a.count > limitCount {
			limitCount = a.count
		}
	}
	digitsLow, digitsCount := 1, 1
	for t := limitLow; t >= 10; t /= 10 {
		digitsLow++
	}
	for t := limitCount; t >= 10; t /= 10 {
		digitsCount++
	}

	kept := out[:0]
	for _, a := range out {
		if a.count == 0 {
			continue
		}
		a.name = fmt.Sprintf("%s.vol%0*d+%0*d.par2", base, digitsLow, a.exponent, digitsCount, a.count)
		kept = append(kept, a)
	}
	return kept
}
