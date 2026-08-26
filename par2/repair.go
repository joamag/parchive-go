package par2

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/joamag/parchive-go/internal/safe"
)

// Options controls how hard the package looks for data before giving up.
type Options struct {
	// NoScan restricts the search to the offsets a slice is supposed to be at.
	// The sliding window only runs once something has already been found wrong,
	// so leaving it on costs nothing on intact data; this exists for callers
	// that would rather fail fast than spend time looking.
	NoScan bool
}

// FileStatus reports the state of one file of the recovery set.
type FileStatus struct {
	File    *FileDesc
	Path    string
	Present bool
	OK      bool

	// Damaged lists slice indices, relative to the file, whose contents could
	// not be found anywhere. These are the ones that need recovery data.
	Damaged []int

	// Misplaced lists slice indices whose contents were found, but not at the
	// offset they belong at. A file with misplaced slices can be rebuilt
	// without spending any recovery data at all.
	Misplaced []int
}

// Verify checks every file of the set against its slice checksums.
//
// Slices are looked for at their natural offsets first. If anything is missing,
// a one-slice window is slid across every file in the set, which finds data
// that moved because bytes were inserted or deleted, and data that ended up in
// a different file of the set entirely.
func (s *Set) Verify(dir string) ([]FileStatus, error) {
	return s.VerifyWith(dir, Options{})
}

// VerifyWith is Verify with explicit options.
func (s *Set) VerifyWith(dir string, o Options) ([]FileStatus, error) {
	found, err := s.locate(dir, !o.NoScan)
	if err != nil {
		return nil, err
	}
	return s.status(dir, found)
}

// status turns a map of located slices into a per-file report.
func (s *Set) status(dir string, found map[int]located) ([]FileStatus, error) {
	out := make([]FileStatus, 0, len(s.Files))
	base := 0
	for _, fd := range s.Files {
		path, err := safe.Join(dir, fd.Name)
		if err != nil {
			return nil, err
		}
		st := FileStatus{File: fd, Path: path}
		if info, err := os.Stat(path); err == nil {
			st.Present = true
			st.OK = uint64(info.Size()) == fd.Size
		}
		want := int64(s.SliceSize)
		for i := range fd.Slices {
			loc, ok := found[base+i]
			switch {
			case !ok:
				st.Damaged = append(st.Damaged, i)
			case loc.path != path || loc.offset != int64(i)*want:
				st.Misplaced = append(st.Misplaced, i)
			}
		}
		if len(st.Damaged) > 0 || len(st.Misplaced) > 0 {
			st.OK = false
		}
		out = append(out, st)
		base += len(fd.Slices)
	}
	return out, nil
}

// Repair rebuilds every damaged slice it can and rewrites the affected files.
// It needs at least as many recovery slices as there are slices that could not
// be found; slices that merely moved cost nothing.
func (s *Set) Repair(dir string) error { return s.RepairWith(dir, Options{}) }

// RepairWith is Repair with explicit options.
func (s *Set) RepairWith(dir string, o Options) error {
	found, err := s.locate(dir, !o.NoScan)
	if err != nil {
		return err
	}
	status, err := s.status(dir, found)
	if err != nil {
		return err
	}

	work := false
	for _, st := range status {
		if !st.OK {
			work = true
			break
		}
	}
	if !work {
		return nil
	}

	var missing []int
	for g, n := 0, s.TotalSlices(); g < n; g++ {
		if _, ok := found[g]; !ok {
			missing = append(missing, g)
		}
	}

	recovered := map[int][]byte{}
	if len(missing) > 0 {
		recovered, err = s.solveMissing(found, missing)
		if err != nil {
			return err
		}
	}
	return s.rewriteAll(status, found, recovered)
}

// solveMissing reconstructs the slices that could not be found, by subtracting
// everything that survived from the recovery slices and solving what is left.
func (s *Set) solveMissing(found map[int]located, missing []int) (map[int][]byte, error) {
	exps := make([]uint32, 0, len(s.Recovery))
	for e := range s.Recovery {
		exps = append(exps, e)
	}
	sort.Slice(exps, func(i, j int) bool { return exps[i] < exps[j] })
	if len(exps) < len(missing) {
		return nil, fmt.Errorf("par2: need %d recovery slices, have %d", len(missing), len(exps))
	}
	exps = exps[:len(missing)]

	consts, err := inputConstants(s.TotalSlices())
	if err != nil {
		return nil, err
	}

	// rhs[r] = recovery[e_r] minus the contribution of every surviving slice.
	rhs := make([][]byte, len(exps))
	for r, e := range exps {
		rhs[r] = append([]byte(nil), s.Recovery[e]...)
	}

	src := newSliceReader(int(s.SliceSize))
	defer src.Close()
	buf := make([]byte, s.SliceSize)
	for _, g := range orderedLocations(found) {
		if err := src.read(found[g], buf); err != nil {
			return nil, err
		}
		for r, e := range exps {
			f := gfPow(consts[g], e)
			if f == 0 {
				continue
			}
			mulAddTables(rhs[r], buf, makeTables(f))
		}
	}

	mat := make([][]uint16, len(exps))
	for r, e := range exps {
		mat[r] = make([]uint16, len(missing))
		for c, g := range missing {
			mat[r][c] = gfPow(consts[g], e)
		}
	}
	if err := solve(mat, rhs); err != nil {
		return nil, err
	}

	out := make(map[int][]byte, len(missing))
	for c, g := range missing {
		out[g] = rhs[c]
	}
	return out, nil
}

// rewriteAll reassembles every file that needs it. Each one is built in a
// temporary file first and only renamed at the very end, so a file that holds
// slices belonging to another one stays readable until every read is done.
func (s *Set) rewriteAll(status []FileStatus, found map[int]located, recovered map[int][]byte) error {
	type pending struct{ tmp, final string }
	var done []pending

	// Best effort: the originals are untouched until the renames at the end, so
	// a leftover temporary file is untidy rather than dangerous.
	cleanup := func() {
		for _, p := range done {
			_ = os.Remove(p.tmp)
		}
	}

	src := newSliceReader(int(s.SliceSize))
	defer src.Close()
	buf := make([]byte, s.SliceSize)

	base := 0
	for i, st := range status {
		fd := s.Files[i]
		if st.OK {
			base += len(fd.Slices)
			continue
		}

		tmp := st.Path + ".par2tmp"
		out, err := os.Create(tmp)
		if err != nil {
			cleanup()
			return err
		}
		written := uint64(0)
		for j := range fd.Slices {
			g := base + j
			var data []byte
			switch loc, ok := found[g]; {
			case ok:
				if err := src.read(loc, buf); err != nil {
					_ = out.Close()
					cleanup()
					_ = os.Remove(tmp)
					return err
				}
				data = buf
			default:
				rec, ok := recovered[g]
				if !ok {
					_ = out.Close()
					cleanup()
					_ = os.Remove(tmp)
					return fmt.Errorf("par2: slice %d of %q could not be recovered", j, fd.Name)
				}
				data = rec
			}
			if n := fd.Size - written; n < s.SliceSize { // trim the padded tail
				data = data[:n]
			}
			if _, err := out.Write(data); err != nil {
				_ = out.Close()
				cleanup()
				_ = os.Remove(tmp)
				return err
			}
			written += uint64(len(data))
		}
		if err := out.Close(); err != nil {
			cleanup()
			_ = os.Remove(tmp)
			return err
		}
		done = append(done, pending{tmp, st.Path})
		base += len(fd.Slices)
	}

	for _, p := range done {
		if err := os.Rename(p.tmp, p.final); err != nil {
			return err
		}
	}
	return nil
}

// orderedLocations lists located slices grouped by file and ascending offset,
// so that reading them back is a forward pass rather than a seek storm.
func orderedLocations(found map[int]located) []int {
	out := make([]int, 0, len(found))
	for g := range found {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := found[out[i]], found[out[j]]
		if a.path != b.path {
			return a.path < b.path
		}
		return a.offset < b.offset
	})
	return out
}

// sliceReader reads slices from wherever they were found, keeping the most
// recently used file open since callers work through one file at a time.
type sliceReader struct {
	size int
	path string
	f    *os.File
}

func newSliceReader(size int) *sliceReader { return &sliceReader{size: size} }

func (r *sliceReader) read(loc located, buf []byte) error {
	if r.f == nil || r.path != loc.path {
		r.Close()
		f, err := os.Open(loc.path)
		if err != nil {
			return err
		}
		r.f, r.path = f, loc.path
	}
	n, err := r.f.ReadAt(buf, loc.offset)
	if err != nil && n == 0 {
		return err
	}
	for i := n; i < len(buf); i++ { // a tail slice is zero padded
		buf[i] = 0
	}
	return nil
}

func (r *sliceReader) Close() {
	if r.f != nil {
		_ = r.f.Close()
		r.f, r.path = nil, ""
	}
}

// solve performs Gauss-Jordan elimination on mat, applying the same row
// operations to the right-hand side slices.
func solve(mat [][]uint16, rhs [][]byte) error {
	n := len(mat)
	for col := 0; col < n; col++ {
		p := -1
		for r := col; r < n; r++ {
			if mat[r][col] != 0 {
				p = r
				break
			}
		}
		if p < 0 {
			return errors.New("par2: recovery matrix is singular")
		}
		mat[col], mat[p] = mat[p], mat[col]
		rhs[col], rhs[p] = rhs[p], rhs[col]

		inv := gfDiv(1, mat[col][col])
		for c := col; c < n; c++ {
			mat[col][c] = gfMul(mat[col][c], inv)
		}
		scale(rhs[col], inv)

		for r := 0; r < n; r++ {
			if r == col || mat[r][col] == 0 {
				continue
			}
			f := mat[r][col]
			for c := col; c < n; c++ {
				mat[r][c] ^= gfMul(mat[col][c], f)
			}
			mulAdd(rhs[r], rhs[col], f)
		}
	}
	return nil
}
