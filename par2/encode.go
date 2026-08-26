package par2

import (
	"bytes"
	"crypto/md5"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"sync"
)

// batchSlices is how many input slices are read before the workers are woken.
// Batching keeps the synchronisation cost per slice small and lets the source
// bytes stay in cache while every recovery slice consumes them, at the price of
// holding batchSlices*sliceSize of input in memory.
const batchSlices = 8

// maxBatchBytes caps that buffer so a large slice size cannot turn into a large
// allocation.
const maxBatchBytes = 8 << 20

// identify reads just enough of a file to derive its ID, which is what decides
// the order of the recovery set. The full contents are read later, once, by the
// encoding pass.
func identify(path string, sliceSize uint64) (*FileDesc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}

	head := make([]byte, 16384)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	fd := &FileDesc{Size: uint64(st.Size()), Name: baseName(path)}
	fd.MD516k = md5.Sum(head[:n])
	fd.ID = FileID(fd.MD516k, fd.Size, fd.Name)
	return fd, nil
}

// encoder carries the state shared by the workers of one Create call.
type encoder struct {
	set       *Set
	sliceSize uint64
	firstExp  uint32
	count     int
	consts    []uint16

	recovery [][]byte // indexed by exponent offset, kept in exponent order
	workers  int
}

// Create builds a recovery set for the given files: count recovery slices with
// exponents firstExp .. firstExp+count-1.
//
// The input is read exactly once. Within each batch the whole-file hash, the
// per-slice checksums and the Reed-Solomon accumulation all run concurrently,
// over the same buffer, since none of them writes to it.
func Create(paths []string, sliceSize uint64, firstExp uint32, count int, creator string) (*Set, error) {
	if sliceSize == 0 || sliceSize%4 != 0 {
		return nil, errors.New("par2: slice size must be a non-zero multiple of 4")
	}
	s := &Set{SliceSize: sliceSize, Recovery: map[uint32][]byte{}, Creator: creator}

	byID := map[[16]byte]string{}
	for _, p := range paths {
		fd, err := identify(p, sliceSize)
		if err != nil {
			return nil, err
		}
		s.Files = append(s.Files, fd)
		byID[fd.ID] = p
	}
	sortFiles(s.Files)
	s.SetID = md5.Sum(s.mainBody())

	consts, err := inputConstants(s.TotalSlices())
	if err != nil {
		return nil, err
	}

	e := &encoder{
		set: s, sliceSize: sliceSize, firstExp: firstExp, count: count,
		consts: consts, workers: runtime.GOMAXPROCS(0),
	}
	if count > 0 {
		e.recovery = make([][]byte, count)
		for i := range e.recovery {
			e.recovery[i] = make([]byte, sliceSize)
			s.Recovery[firstExp+uint32(i)] = e.recovery[i]
		}
	}

	global := 0
	for _, fd := range s.Files {
		n, err := e.file(byID[fd.ID], fd, global)
		if err != nil {
			return nil, err
		}
		global += n
	}
	return s, nil
}

// file streams one input file, hashing and encoding it in a single pass, and
// returns how many slices it contributed.
func (e *encoder) file(path string, fd *FileDesc, base int) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	per := batchSlices
	if e.sliceSize > 0 {
		if n := int(maxBatchBytes / e.sliceSize); n < per {
			per = n
		}
		if per < 1 {
			per = 1
		}
	}
	buf := make([]byte, uint64(per)*e.sliceSize)

	total := fd.sliceCount(e.sliceSize)
	fd.Slices = make([]SliceHash, total)
	whole := md5.New()

	done := 0
	for done < total {
		n := per
		if r := total - done; r < n {
			n = r
		}
		window := buf[:uint64(n)*e.sliceSize]

		read, err := io.ReadFull(f, window)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return 0, err
		}
		for i := read; i < len(window); i++ { // zero pad the tail slice
			window[i] = 0
		}

		var wg sync.WaitGroup

		// The whole-file hash is a single chain, so it stays on one goroutine
		// and only sees the bytes the file actually has.
		wg.Add(1)
		go func() {
			defer wg.Done()
			whole.Write(window[:read])
		}()

		// Per-slice checksums are independent of each other.
		e.fanOut(&wg, n, func(lo, hi int) {
			for i := lo; i < hi; i++ {
				sl := window[uint64(i)*e.sliceSize : uint64(i+1)*e.sliceSize]
				fd.Slices[done+i] = SliceHash{MD5: md5.Sum(sl), CRC32: crc32.ChecksumIEEE(sl)}
			}
		})

		// Recovery slices are partitioned by exponent, so no two workers ever
		// touch the same output buffer.
		if e.count > 0 {
			e.fanOut(&wg, e.count, func(lo, hi int) {
				for x := lo; x < hi; x++ {
					exp := e.firstExp + uint32(x)
					dst := e.recovery[x]
					for i := 0; i < n; i++ {
						f := gfPow(e.consts[base+done+i], exp)
						if f == 0 {
							continue
						}
						sl := window[uint64(i)*e.sliceSize : uint64(i+1)*e.sliceSize]
						if f == 1 {
							xorInto(dst, sl)
							continue
						}
						mulAddTables(dst, sl, makeTables(f))
					}
				}
			})
		}

		wg.Wait()
		done += n
		if read < len(window) {
			break
		}
	}

	copy(fd.MD5[:], whole.Sum(nil))
	return total, nil
}

// fanOut splits [0,n) across the worker budget and runs body on each part.
func (e *encoder) fanOut(wg *sync.WaitGroup, n int, body func(lo, hi int)) {
	parts := e.workers
	if parts > n {
		parts = n
	}
	if parts <= 1 {
		wg.Add(1)
		go func() { defer wg.Done(); body(0, n) }()
		return
	}
	step := (n + parts - 1) / parts
	for lo := 0; lo < n; lo += step {
		hi := lo + step
		if hi > n {
			hi = n
		}
		wg.Add(1)
		go func(lo, hi int) { defer wg.Done(); body(lo, hi) }(lo, hi)
	}
}

func xorInto(dst, src []byte) {
	for i := range src {
		dst[i] ^= src[i]
	}
}

func sortFiles(files []*FileDesc) {
	// Insertion sort keeps the ordering rule visible and the set is tiny.
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && bytes.Compare(files[j].ID[:], files[j-1].ID[:]) < 0; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
}
