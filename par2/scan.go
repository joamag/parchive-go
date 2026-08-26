package par2

import (
	"crypto/md5"
	"hash/crc32"
	"io"
	"math/bits"
	"os"

	"github.com/joamag/parchive-go/internal/safe"
)

// located records where a good copy of one input slice was found. Slices are
// identified by their global index, so a slice that drifted into a different
// file of the set is just as findable as one that stayed put.
type located struct {
	path   string
	offset int64
}

// scanChunk is how much of a file is examined at a time. The window has to be
// able to straddle a chunk boundary, so consecutive chunks overlap by one slice
// less a byte; a chunk several times the slice size keeps that overhead small.
const scanChunk = 4 << 20

// locator holds the index used to recognise a slice by content.
type locator struct {
	set    *Set
	size   int
	roll   *rolling
	hashes []SliceHash
	byCRC  map[uint32][]int

	// filter rejects the overwhelming majority of window positions with a
	// single memory access. Going to the map for every byte would dominate the
	// scan, so the map is only consulted once a CRC looks plausible.
	filter []uint64
	fmask  uint32
}

func newLocator(s *Set) *locator {
	total := s.TotalSlices()
	l := &locator{
		set:    s,
		size:   int(s.SliceSize),
		roll:   newRolling(int(s.SliceSize)),
		hashes: make([]SliceHash, 0, total),
		byCRC:  make(map[uint32][]int, total),
	}
	for _, fd := range s.Files {
		l.hashes = append(l.hashes, fd.Slices...)
	}
	for g, h := range l.hashes {
		l.byCRC[h.CRC32] = append(l.byCRC[h.CRC32], g)
	}

	// Size the filter so a false positive is rare even for a large set, but
	// keep it small enough to stay resident in cache.
	bitsWanted := uint32(64 * (total + 1))
	if bitsWanted > 1<<22 {
		bitsWanted = 1 << 22
	}
	size := uint32(1) << uint(bits.Len32(bitsWanted-1))
	if size < 1<<12 {
		size = 1 << 12
	}
	l.fmask = size - 1
	l.filter = make([]uint64, size/64)
	for _, h := range l.hashes {
		i := h.CRC32 & l.fmask
		l.filter[i>>6] |= 1 << (i & 63)
	}
	return l
}

// match reports the global slice index whose checksums equal the given window,
// or -1. Candidates are only accepted on the MD5, so a CRC collision cannot
// cause the wrong slice to be recognised.
//
// A slice that has already been found still matches: knowing the window holds a
// known slice is what lets the scan step over it rather than crawling through
// it a byte at a time.
func (l *locator) match(crc uint32, window []byte) int {
	cands := l.byCRC[crc]
	if len(cands) == 0 {
		return -1
	}
	sum := md5.Sum(window)
	for _, g := range cands {
		if l.hashes[g].MD5 == sum {
			return g
		}
	}
	return -1
}

// aligned checks a file at the offsets the set says its slices should be at.
// This is the fast path and, on undamaged data, the only one that runs.
func (l *locator) aligned(path string, base, count int, found map[int]located, known map[int64]bool) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, nil // absent files are handled by the scan
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, l.size)
	complete := true
	for i := 0; i < count; i++ {
		n, err := io.ReadFull(f, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return false, err
		}
		for j := n; j < len(buf); j++ {
			buf[j] = 0
		}
		g := base + i
		if l.hashes[g].CRC32 == crc32.ChecksumIEEE(buf) && l.hashes[g].MD5 == md5.Sum(buf) {
			off := int64(i) * int64(l.size)
			found[g] = located{path: path, offset: off}
			known[off] = true
			continue
		}
		complete = false
		if n < len(buf) {
			break
		}
	}
	return complete, nil
}

// scan slides a one-slice window across a file looking for any slice of the set
// that has not been found yet. This is what recovers from insertions and
// deletions, where every slice after the edit sits at the wrong offset.
func (l *locator) scan(path string, found map[int]located, known map[int64]bool) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	size := l.size
	chunk := scanChunk
	if chunk < 4*size {
		chunk = 4 * size
	}
	// The tail of a file is a zero padded slice, so the buffer carries room for
	// the window to run past the last byte rather than stopping short of it.
	buf := make([]byte, chunk+size)

	var base int64
	for {
		if _, err := f.Seek(base, io.SeekStart); err != nil {
			return err
		}
		n, err := io.ReadFull(f, buf[:chunk])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		if n == 0 {
			return nil
		}

		if n < chunk { // end of the file
			for i := n; i < n+size; i++ {
				buf[i] = 0
			}
			l.scanBuffer(path, buf[:n+size], base, n-1, found, known)
			return nil
		}
		l.scanBuffer(path, buf[:n], base, n-size, found, known)
		base += int64(n - size + 1)
	}
}

// scanBuffer walks every window position from zero up to last inclusive.
//
// The loop is written out rather than calling through rolling because it runs
// once per byte of every file being searched. Whole-window checksums go through
// hash/crc32, which is hardware accelerated, and only the single byte steps use
// the incremental update.
func (l *locator) scanBuffer(path string, buf []byte, base int64, last int, found map[int]located, known map[int64]bool) {
	size := l.size
	if last < 0 || last+size > len(buf) {
		return
	}
	var (
		table  = crcTable
		window = &l.roll.window
		mask   = l.roll.mask
		filter = l.filter
		fmask  = l.fmask
	)

	i := 0
	raw := crc32.ChecksumIEEE(buf[:size]) ^ mask
	for {
		crc := raw ^ mask
		if k := crc & fmask; filter[k>>6]&(1<<(k&63)) != 0 {
			// The aligned pass already hashed this offset and found a good
			// slice there, so step over it without paying for the MD5 again.
			if known[base+int64(i)] {
				i += size
				if i > last {
					return
				}
				raw = crc32.ChecksumIEEE(buf[i:i+size]) ^ mask
				continue
			}
			if g := l.match(crc, buf[i:i+size]); g >= 0 {
				if _, done := found[g]; !done {
					found[g] = located{path: path, offset: base + int64(i)}
				}
				// Slices do not overlap, so the next one can only start after
				// this one ends. Skipping avoids rolling through data that has
				// already been accounted for.
				i += size
				if i > last {
					return
				}
				raw = crc32.ChecksumIEEE(buf[i:i+size]) ^ mask
				continue
			}
		}
		if i == last {
			return
		}
		raw = table[byte(raw)^buf[i+size]] ^ (raw >> 8) ^ window[buf[i]]
		i++
	}
}

// locate works out where every slice of the set currently lives. It checks the
// natural offsets first and only slides a window if something is missing, so an
// intact set costs exactly what it did before.
func (s *Set) locate(dir string, scan bool) (map[int]located, error) {
	l := newLocator(s)
	found := make(map[int]located, len(l.hashes))

	type entry struct {
		path     string
		known    map[int64]bool
		complete bool
	}
	var files []entry
	base := 0
	complete := true
	for _, fd := range s.Files {
		path, err := safe.Join(dir, fd.Name)
		if err != nil {
			return nil, err
		}
		e := entry{path: path, known: map[int64]bool{}}
		ok, err := l.aligned(path, base, len(fd.Slices), found, e.known)
		if err != nil {
			return nil, err
		}
		// A file whose every slice checked out at its own offset, and whose
		// length is exactly what the set records, has no room left to hide
		// anything: every byte of it belongs to a slice already accounted for.
		if info, serr := os.Stat(path); ok && serr == nil && uint64(info.Size()) == fd.Size {
			e.complete = true
		} else {
			complete = false
		}
		files = append(files, e)
		base += len(fd.Slices)
	}
	if complete || !scan || len(found) == len(l.hashes) {
		return found, nil
	}

	// Something is missing. Sweep the files that could still be hiding it,
	// including ones that verified cleanly apart from their length, because a
	// slice can drift out of one file and into another.
	for _, e := range files {
		if e.complete || len(found) == len(l.hashes) {
			continue
		}
		if err := l.scan(e.path, found, e.known); err != nil {
			return nil, err
		}
	}
	return found, nil
}
