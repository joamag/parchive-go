// Package par1 implements the PAR1 (Parity Volume Set 1.0) file format:
// volume headers, file entries and Reed-Solomon coding over GF(2^8), plus
// creation, verification and repair of recovery sets.
//
// PAR1 protects whole files rather than slices: every input file is one shard,
// zero padded to the length of the largest, and each recovery volume holds a
// single parity shard. That makes it much coarser than PAR2 - losing two files
// costs two volumes no matter how small they were - but it is also the format
// that the original par utility and QuickPar wrote, so old sets still need it.
package par1

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf16"

	"github.com/joamag/parchive-go/internal/safe"
)

// ---------------------------------------------------------------------------
// GF(2^8) — primitive polynomial 0x11D, as used by PAR1.
// ---------------------------------------------------------------------------

const gfPoly = 0x11D

var (
	gfExp [512]byte // gfExp[k] = 2^k, doubled so products need no modulo
	gfLog [256]byte // gfLog[2^k] = k
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= gfPoly
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func gfDiv(a, b byte) byte {
	if a == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+255-int(gfLog[b])]
}

func gfPow(a byte, n int) byte {
	if n == 0 {
		return 1
	}
	if a == 0 {
		return 0
	}
	return gfExp[(int(gfLog[a])*n)%255]
}

// MaxFiles is the number of input files PAR1 can address: the coefficient of
// file i is (i+1), which has to stay inside GF(2^8).
const MaxFiles = 255

// coefficient returns the constant that file index i contributes to a parity
// volume raised to the volume's exponent.
func coefficient(i int) byte { return byte(i + 1) }

// mulAdd computes dst ^= factor * src, byte by byte.
func mulAdd(dst, src []byte, factor byte) {
	if factor == 0 {
		return
	}
	if factor == 1 {
		for i := range src {
			dst[i] ^= src[i]
		}
		return
	}
	lf := int(gfLog[factor])
	for i := range src {
		if v := src[i]; v != 0 {
			dst[i] ^= gfExp[int(gfLog[v])+lf]
		}
	}
}

// scale computes buf = factor * buf.
func scale(buf []byte, factor byte) {
	if factor == 1 {
		return
	}
	lf := int(gfLog[factor])
	for i := range buf {
		if v := buf[i]; v != 0 {
			buf[i] = gfExp[int(gfLog[v])+lf]
		}
	}
}

// ---------------------------------------------------------------------------
// Volume header
// ---------------------------------------------------------------------------

var magic = [8]byte{'P', 'A', 'R', 0, 0, 0, 0, 0}

// headerSize is the fixed 0x60 byte prologue every PAR1 volume starts with.
const headerSize = 0x60

// Version is the format version stamped into every volume this package writes.
const Version = 0x00010000

// Status bits carried by each file entry.
const (
	StatusInSet   = 1 << 0 // file contributes to the parity data
	StatusChecked = 1 << 1 // file was verified when the set was written
)

// FileEntry describes one file of the recovery set.
type FileEntry struct {
	Status uint64
	Size   uint64
	MD5    [16]byte // whole file
	MD516k [16]byte // first 16 KiB (or whole file if shorter)
	Name   string
}

// entrySize is the on-disk size of the entry, header plus UTF-16 name.
func (f *FileEntry) entrySize() uint64 {
	return 0x38 + uint64(len(utf16.Encode([]rune(f.Name))))*2
}

// InSet reports whether the file contributes to the parity data.
func (f *FileEntry) InSet() bool { return f.Status&StatusInSet != 0 }

// Set is a complete PAR1 recovery set.
type Set struct {
	SetHash    [16]byte
	Files      []*FileEntry
	Recovery   map[uint64][]byte // volume number (1 based) -> parity shard
	VolumeSize uint64            // length of every shard: the largest file
	Client     uint32
}

// setHash is the MD5 of the concatenated file hashes of every file in the set,
// in file-list order. It identifies the recovery set across its volumes.
func (s *Set) setHash() [16]byte {
	h := md5.New()
	for _, f := range s.Files {
		if f.InSet() {
			h.Write(f.MD5[:])
		}
	}
	var out [16]byte
	copy(out[:], h.Sum(nil))
	return out
}

// dataShards returns the indices of the files that carry parity, which are the
// columns of the Reed-Solomon matrix.
func (s *Set) dataShards() []int {
	var out []int
	for i, f := range s.Files {
		if f.InSet() {
			out = append(out, i)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Creation
// ---------------------------------------------------------------------------

// Describe hashes one input file and produces its file entry.
func Describe(path string) (*FileEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}

	fe := &FileEntry{
		Status: StatusInSet | StatusChecked,
		Size:   uint64(st.Size()),
		Name:   filepath.Base(path),
	}
	whole := md5.New()
	first := md5.New()
	buf := make([]byte, 64*1024)
	remaining16k := int64(16384)

	for {
		n, err := f.Read(buf)
		if n > 0 {
			whole.Write(buf[:n])
			if remaining16k > 0 {
				k := int64(n)
				if k > remaining16k {
					k = remaining16k
				}
				first.Write(buf[:k])
				remaining16k -= k
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	copy(fe.MD5[:], whole.Sum(nil))
	copy(fe.MD516k[:], first.Sum(nil))
	return fe, nil
}

// Create builds a recovery set for the given files, with count recovery
// volumes numbered 1..count.
func Create(paths []string, count int, client uint32) (*Set, error) {
	if len(paths) == 0 {
		return nil, errors.New("par1: no input files")
	}
	if len(paths) > MaxFiles {
		return nil, fmt.Errorf("par1: %d files exceeds the limit of %d", len(paths), MaxFiles)
	}
	if count < 0 || len(paths)+count > MaxFiles+1 {
		return nil, fmt.Errorf("par1: %d files plus %d volumes exceeds the 256 shard limit", len(paths), count)
	}

	s := &Set{Recovery: map[uint64][]byte{}, Client: client}
	for _, p := range paths {
		fe, err := Describe(p)
		if err != nil {
			return nil, err
		}
		s.Files = append(s.Files, fe)
		if fe.Size > s.VolumeSize {
			s.VolumeSize = fe.Size
		}
	}
	// PAR1 keeps the file list in the order it was given; the set hash is what
	// ties the volumes together, so there is nothing to sort.
	s.SetHash = s.setHash()
	if count == 0 {
		return s, nil
	}

	for v := 1; v <= count; v++ {
		s.Recovery[uint64(v)] = make([]byte, s.VolumeSize)
	}

	// Every file is one shard, zero padded to VolumeSize. Volume v holds
	// sum over files of (i+1)^(v-1) * data_i.
	buf := make([]byte, s.VolumeSize)
	for col, idx := range s.dataShards() {
		f, err := os.Open(paths[idx])
		if err != nil {
			return nil, err
		}
		n, err := io.ReadFull(f, buf)
		f.Close()
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		for j := n; j < len(buf); j++ {
			buf[j] = 0
		}
		for v := 1; v <= count; v++ {
			mulAdd(s.Recovery[uint64(v)], buf, gfPow(coefficient(col), v-1))
		}
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Serialisation
// ---------------------------------------------------------------------------

// fileList serialises every file entry.
func (s *Set) fileList() []byte {
	var b bytes.Buffer
	for _, f := range s.Files {
		name := utf16.Encode([]rune(f.Name))
		binary.Write(&b, binary.LittleEndian, f.entrySize())
		binary.Write(&b, binary.LittleEndian, f.Status)
		binary.Write(&b, binary.LittleEndian, f.Size)
		b.Write(f.MD5[:])
		b.Write(f.MD516k[:])
		for _, u := range name {
			binary.Write(&b, binary.LittleEndian, u)
		}
	}
	return b.Bytes()
}

// write emits one volume: the header, the file list and the given payload.
// A volume number of 0 writes the index file, which carries no parity data.
func (s *Set) write(w io.Writer, volume uint64, data []byte) error {
	list := s.fileList()
	buf := make([]byte, headerSize+len(list)+len(data))

	copy(buf, magic[:])
	binary.LittleEndian.PutUint32(buf[0x08:], Version)
	binary.LittleEndian.PutUint32(buf[0x0C:], s.Client)
	copy(buf[0x20:], s.SetHash[:])
	binary.LittleEndian.PutUint64(buf[0x30:], volume)
	binary.LittleEndian.PutUint64(buf[0x38:], uint64(len(s.Files)))
	binary.LittleEndian.PutUint64(buf[0x40:], headerSize)
	binary.LittleEndian.PutUint64(buf[0x48:], uint64(len(list)))
	binary.LittleEndian.PutUint64(buf[0x50:], uint64(headerSize+len(list)))
	binary.LittleEndian.PutUint64(buf[0x58:], uint64(len(data)))
	copy(buf[headerSize:], list)
	copy(buf[headerSize+len(list):], data)

	// The control hash covers everything from the set hash onwards, so it has
	// to be filled in last.
	sum := md5.Sum(buf[0x20:])
	copy(buf[0x10:], sum[:])

	_, err := w.Write(buf)
	return err
}

// WriteIndex writes the index volume: the file list, with no parity data.
func (s *Set) WriteIndex(w io.Writer) error {
	return s.write(w, 0, nil)
}

// WriteVolume writes recovery volume number v, which must be present in the
// set's recovery data.
func (s *Set) WriteVolume(w io.Writer, v uint64) error {
	data, ok := s.Recovery[v]
	if !ok {
		return fmt.Errorf("par1: no recovery data for volume %d", v)
	}
	return s.write(w, v, data)
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// parseVolume reads a single PAR1 file, returning its volume number, file list
// and parity payload.
func parseVolume(data []byte) (volume uint64, setHash [16]byte, files []*FileEntry, payload []byte, err error) {
	if len(data) < headerSize || !bytes.Equal(data[:8], magic[:]) {
		return 0, setHash, nil, nil, errors.New("par1: not a PAR1 volume")
	}
	sum := md5.Sum(data[0x20:])
	if !bytes.Equal(sum[:], data[0x10:0x20]) {
		return 0, setHash, nil, nil, errors.New("par1: control hash mismatch")
	}
	copy(setHash[:], data[0x20:0x30])
	volume = binary.LittleEndian.Uint64(data[0x30:])
	count := binary.LittleEndian.Uint64(data[0x38:])
	listOff := binary.LittleEndian.Uint64(data[0x40:])
	listSize := binary.LittleEndian.Uint64(data[0x48:])
	dataOff := binary.LittleEndian.Uint64(data[0x50:])
	dataSize := binary.LittleEndian.Uint64(data[0x58:])

	if listOff+listSize > uint64(len(data)) || dataOff+dataSize > uint64(len(data)) {
		return 0, setHash, nil, nil, errors.New("par1: volume is truncated")
	}

	list := data[listOff : listOff+listSize]
	for off := uint64(0); off < uint64(len(list)) && uint64(len(files)) < count; {
		if off+0x38 > uint64(len(list)) {
			return 0, setHash, nil, nil, errors.New("par1: file entry is truncated")
		}
		size := binary.LittleEndian.Uint64(list[off:])
		if size < 0x38 || off+size > uint64(len(list)) {
			return 0, setHash, nil, nil, errors.New("par1: file entry has a bad length")
		}
		fe := &FileEntry{
			Status: binary.LittleEndian.Uint64(list[off+0x08:]),
			Size:   binary.LittleEndian.Uint64(list[off+0x10:]),
		}
		copy(fe.MD5[:], list[off+0x18:])
		copy(fe.MD516k[:], list[off+0x28:])

		raw := list[off+0x38 : off+size]
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(raw[i*2:])
		}
		fe.Name = string(utf16.Decode(units))
		files = append(files, fe)
		off += size
	}
	return volume, setHash, files, data[dataOff : dataOff+dataSize], nil
}

// Parse reconstructs a Set from one or more PAR1 volume files. Volumes that
// belong to a different set, or whose control hash fails, are skipped.
func Parse(paths ...string) (*Set, error) {
	s := &Set{Recovery: map[uint64][]byte{}}
	have := false

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		volume, setHash, files, payload, err := parseVolume(data)
		if err != nil {
			continue // damaged or unrelated volume, try the next one
		}
		if have && setHash != s.SetHash {
			continue
		}
		if !have {
			s.SetHash, s.Files, have = setHash, files, true
		}
		if volume > 0 && len(payload) > 0 {
			if _, seen := s.Recovery[volume]; !seen {
				s.Recovery[volume] = payload
			}
			if uint64(len(payload)) > s.VolumeSize {
				s.VolumeSize = uint64(len(payload))
			}
		}
	}
	if !have {
		return nil, errors.New("par1: no readable PAR1 volume found")
	}
	if s.VolumeSize == 0 {
		for _, f := range s.Files {
			if f.Size > s.VolumeSize {
				s.VolumeSize = f.Size
			}
		}
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Verification
// ---------------------------------------------------------------------------

// FileStatus reports the state of one file of the recovery set.
type FileStatus struct {
	File    *FileEntry
	Path    string
	Present bool
	OK      bool
}

// Verify checks every file of the set against its recorded hashes.
func (s *Set) Verify(dir string) ([]FileStatus, error) {
	var out []FileStatus
	for _, fe := range s.Files {
		path, err := safe.Join(dir, fe.Name)
		if err != nil {
			return nil, err
		}
		st := FileStatus{File: fe, Path: path}

		data, err := os.ReadFile(path)
		if err == nil {
			st.Present = true
			st.OK = uint64(len(data)) == fe.Size && md5.Sum(data) == fe.MD5
		}
		out = append(out, st)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Repair
// ---------------------------------------------------------------------------

// Repair rebuilds every damaged or missing file it can and rewrites them. It
// needs at least as many recovery volumes as there are files to rebuild.
func (s *Set) Repair(dir string) error {
	status, err := s.Verify(dir)
	if err != nil {
		return err
	}

	shards := s.dataShards()
	column := make(map[int]int, len(shards)) // file index -> matrix column
	for c, idx := range shards {
		column[idx] = c
	}

	var missing []int // columns that need rebuilding
	for i, st := range status {
		c, ok := column[i]
		if !ok || st.OK {
			continue
		}
		missing = append(missing, c)
	}
	if len(missing) == 0 {
		return nil
	}

	// Prefer the lowest numbered volumes: their exponents are consecutive, so
	// the coefficient matrix stays a plain Vandermonde and cannot be singular.
	var volumes []uint64
	for v := uint64(1); len(volumes) < len(missing) && v <= uint64(MaxFiles); v++ {
		if _, ok := s.Recovery[v]; ok {
			volumes = append(volumes, v)
		}
	}
	if len(volumes) < len(missing) {
		return fmt.Errorf("par1: need %d recovery volumes, have %d", len(missing), len(s.Recovery))
	}

	isMissing := make(map[int]int, len(missing))
	for j, c := range missing {
		isMissing[c] = j
	}

	// rhs[r] = volume_r minus the contribution of every surviving file.
	rhs := make([][]byte, len(volumes))
	for r, v := range volumes {
		row := make([]byte, s.VolumeSize)
		copy(row, s.Recovery[v])
		rhs[r] = row
	}
	buf := make([]byte, s.VolumeSize)
	for c, idx := range shards {
		if _, bad := isMissing[c]; bad {
			continue
		}
		f, err := os.Open(status[idx].Path)
		if err != nil {
			return err
		}
		n, err := io.ReadFull(f, buf)
		f.Close()
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		for j := n; j < len(buf); j++ {
			buf[j] = 0
		}
		for r, v := range volumes {
			mulAdd(rhs[r], buf, gfPow(coefficient(c), int(v-1)))
		}
	}

	mat := make([][]byte, len(volumes))
	for r, v := range volumes {
		mat[r] = make([]byte, len(missing))
		for j, c := range missing {
			mat[r][j] = gfPow(coefficient(c), int(v-1))
		}
	}
	if err := solve(mat, rhs); err != nil {
		return err
	}

	// rhs now holds the rebuilt shards, in the order of `missing`.
	for i, st := range status {
		c, ok := column[i]
		if !ok || st.OK {
			continue
		}
		j := isMissing[c]
		if err := rewrite(st.Path, s.Files[i], rhs[j]); err != nil {
			return err
		}
	}
	return nil
}

// solve performs Gauss-Jordan elimination on mat, applying the same row
// operations to the right-hand side shards.
func solve(mat [][]byte, rhs [][]byte) error {
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
			return errors.New("par1: recovery matrix is singular")
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

// rewrite writes a rebuilt shard back out, trimming the zero padding that was
// added when the set was created.
func rewrite(path string, fe *FileEntry, data []byte) error {
	if uint64(len(data)) < fe.Size {
		return fmt.Errorf("par1: rebuilt shard for %q is too short", fe.Name)
	}
	body := data[:fe.Size]
	if md5.Sum(body) != fe.MD5 {
		return fmt.Errorf("par1: rebuilt %q does not match its recorded hash", fe.Name)
	}
	tmp := path + ".par1tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
