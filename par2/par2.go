// Package par2 implements the PAR2 (Parity Volume Set) file format:
// packet serialisation, Reed-Solomon coding over GF(2^16), plus creation,
// verification and repair of recovery sets.
package par2

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// Packets
// ---------------------------------------------------------------------------

var magic = [8]byte{'P', 'A', 'R', '2', 0, 'P', 'K', 'T'}

const headerSize = 64

func ptype(s string) [16]byte {
	var t [16]byte
	copy(t[:], s)
	return t
}

var (
	TypeMain     = ptype("PAR 2.0\x00Main")
	TypeFileDesc = ptype("PAR 2.0\x00FileDesc")
	TypeIFSC     = ptype("PAR 2.0\x00IFSC")
	TypeRecovery = ptype("PAR 2.0\x00RecvSlic")
	TypeCreator  = ptype("PAR 2.0\x00Creator")
)

// Packet is a single PAR2 packet: a 64-byte header plus a body whose length is
// always a multiple of 4.
type Packet struct {
	SetID [16]byte
	Type  [16]byte
	Body  []byte
}

// Bytes serialises the packet, padding the body and filling in the MD5 of
// everything from the set ID onwards.
func (p Packet) Bytes() []byte {
	n := len(p.Body)
	if r := n % 4; r != 0 {
		n += 4 - r
	}
	buf := make([]byte, headerSize+n)
	copy(buf, magic[:])
	binary.LittleEndian.PutUint64(buf[8:], uint64(len(buf)))
	copy(buf[32:], p.SetID[:])
	copy(buf[48:], p.Type[:])
	copy(buf[64:], p.Body)
	sum := md5.Sum(buf[32:])
	copy(buf[16:], sum[:])
	return buf
}

// ReadPackets scans a buffer for well-formed packets. Damaged packets are
// skipped: the scan resumes at the byte after the failed magic sequence, which
// is what lets PAR2 files survive partial corruption.
func ReadPackets(data []byte) []Packet {
	var out []Packet
	for off := 0; off+headerSize <= len(data); {
		i := bytes.Index(data[off:], magic[:])
		if i < 0 {
			break
		}
		off += i
		if off+headerSize > len(data) {
			break
		}
		length := binary.LittleEndian.Uint64(data[off+8:])
		if length < headerSize || length%4 != 0 || uint64(len(data)-off) < length {
			off += 8
			continue
		}
		raw := data[off : off+int(length)]
		sum := md5.Sum(raw[32:])
		if !bytes.Equal(sum[:], raw[16:32]) {
			off += 8
			continue
		}
		var p Packet
		copy(p.SetID[:], raw[32:48])
		copy(p.Type[:], raw[48:64])
		p.Body = append([]byte(nil), raw[64:]...)
		out = append(out, p)
		off += int(length)
	}
	return out
}

// ---------------------------------------------------------------------------
// Recovery set model
// ---------------------------------------------------------------------------

// SliceHash holds the two checksums PAR2 keeps for each input slice.
type SliceHash struct {
	MD5   [16]byte
	CRC32 uint32
}

// FileDesc describes one file of the recovery set.
type FileDesc struct {
	ID     [16]byte
	MD5    [16]byte // whole file
	MD516k [16]byte // first 16 KiB (or whole file if shorter)
	Size   uint64
	Name   string
	Slices []SliceHash
}

// Set is a complete PAR2 recovery set.
type Set struct {
	SetID     [16]byte
	SliceSize uint64
	Files     []*FileDesc       // sorted by file ID: defines global slice order
	Recovery  map[uint32][]byte // exponent -> recovery slice
	Creator   string
}

// FileID derives the file ID from the 16k hash, the size and the name.
func FileID(md516k [16]byte, size uint64, name string) [16]byte {
	h := md5.New()
	h.Write(md516k[:])
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], size)
	h.Write(b[:])
	h.Write([]byte(name))
	var id [16]byte
	copy(id[:], h.Sum(nil))
	return id
}

func (f *FileDesc) sliceCount(sliceSize uint64) int {
	return int((f.Size + sliceSize - 1) / sliceSize)
}

// TotalSlices is the number of input slices across the whole set.
func (s *Set) TotalSlices() int {
	n := 0
	for _, f := range s.Files {
		n += f.sliceCount(s.SliceSize)
	}
	return n
}

// ---------------------------------------------------------------------------
// Creation
// ---------------------------------------------------------------------------

// Describe hashes one input file and produces its file description packet data
// (including per-slice checksums).
func Describe(path string, sliceSize uint64) (*FileDesc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}

	fd := &FileDesc{Size: uint64(st.Size()), Name: baseName(path)}
	whole := md5.New()
	first := md5.New()
	remaining16k := int64(16384)
	buf := make([]byte, sliceSize)

	for {
		n, err := io.ReadFull(f, buf)
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
			for i := n; i < len(buf); i++ { // zero pad the tail slice
				buf[i] = 0
			}
			fd.Slices = append(fd.Slices, SliceHash{
				MD5:   md5.Sum(buf),
				CRC32: crc32.ChecksumIEEE(buf),
			})
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	copy(fd.MD5[:], whole.Sum(nil))
	copy(fd.MD516k[:], first.Sum(nil))
	fd.ID = FileID(fd.MD516k, fd.Size, fd.Name)
	return fd, nil
}

// ---------------------------------------------------------------------------
// Serialisation
// ---------------------------------------------------------------------------

func (s *Set) mainBody() []byte {
	var b bytes.Buffer
	// Writing to a bytes.Buffer cannot fail.
	_ = binary.Write(&b, binary.LittleEndian, s.SliceSize)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(s.Files)))
	for _, f := range s.Files {
		b.Write(f.ID[:])
	}
	return b.Bytes()
}

func (s *Set) criticalPackets() []Packet {
	pk := []Packet{{SetID: s.SetID, Type: TypeMain, Body: s.mainBody()}}
	for _, f := range s.Files {
		var b bytes.Buffer
		b.Write(f.ID[:])
		b.Write(f.MD5[:])
		b.Write(f.MD516k[:])
		_ = binary.Write(&b, binary.LittleEndian, f.Size)
		b.WriteString(f.Name)
		pk = append(pk, Packet{SetID: s.SetID, Type: TypeFileDesc, Body: b.Bytes()})

		var c bytes.Buffer
		c.Write(f.ID[:])
		for _, sl := range f.Slices {
			c.Write(sl.MD5[:])
			_ = binary.Write(&c, binary.LittleEndian, sl.CRC32)
		}
		pk = append(pk, Packet{SetID: s.SetID, Type: TypeIFSC, Body: c.Bytes()})
	}
	if s.Creator != "" {
		pk = append(pk, Packet{SetID: s.SetID, Type: TypeCreator, Body: []byte(s.Creator)})
	}
	return pk
}

// WriteIndex writes the index file: all critical packets, no recovery data.
func (s *Set) WriteIndex(w io.Writer) error {
	for _, p := range s.criticalPackets() {
		if _, err := w.Write(p.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// WriteVolume writes a recovery volume holding the given exponents, with the
// critical packets repeated so the volume is self-describing.
func (s *Set) WriteVolume(w io.Writer, exponents []uint32) error {
	crit := s.criticalPackets()
	for i, e := range exponents {
		data, ok := s.Recovery[e]
		if !ok {
			return fmt.Errorf("par2: no recovery slice for exponent %d", e)
		}
		body := make([]byte, 4+len(data))
		binary.LittleEndian.PutUint32(body, e)
		copy(body[4:], data)
		if _, err := w.Write(Packet{SetID: s.SetID, Type: TypeRecovery, Body: body}.Bytes()); err != nil {
			return err
		}
		if _, err := w.Write(crit[i%len(crit)].Bytes()); err != nil { // interleave criticals
			return err
		}
	}
	for _, p := range crit {
		if _, err := w.Write(p.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// Parse reconstructs a Set from one or more PAR2 files.
func Parse(paths ...string) (*Set, error) {
	s := &Set{Recovery: map[uint32][]byte{}}
	seen := map[[16]byte]*FileDesc{}
	var order [][16]byte
	haveMain := false

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		for _, pk := range ReadPackets(data) {
			if haveMain && pk.SetID != s.SetID {
				continue // packet from a different recovery set
			}
			switch pk.Type {
			case TypeMain:
				if haveMain || len(pk.Body) < 12 {
					continue
				}
				s.SetID, haveMain = pk.SetID, true
				s.SliceSize = binary.LittleEndian.Uint64(pk.Body)
				n := int(binary.LittleEndian.Uint32(pk.Body[8:]))
				for i := 0; i < n && 12+16*(i+1) <= len(pk.Body); i++ {
					var id [16]byte
					copy(id[:], pk.Body[12+16*i:])
					order = append(order, id)
				}
			case TypeFileDesc:
				if len(pk.Body) < 56 {
					continue
				}
				fd := &FileDesc{Size: binary.LittleEndian.Uint64(pk.Body[48:])}
				copy(fd.ID[:], pk.Body[0:])
				copy(fd.MD5[:], pk.Body[16:])
				copy(fd.MD516k[:], pk.Body[32:])
				fd.Name = string(bytes.TrimRight(pk.Body[56:], "\x00"))
				if old, ok := seen[fd.ID]; ok {
					fd.Slices = old.Slices
				}
				seen[fd.ID] = fd
			case TypeIFSC:
				if len(pk.Body) < 16 {
					continue
				}
				var id [16]byte
				copy(id[:], pk.Body)
				fd, ok := seen[id]
				if !ok {
					fd = &FileDesc{ID: id}
					seen[id] = fd
				}
				if len(fd.Slices) > 0 {
					continue
				}
				for off := 16; off+20 <= len(pk.Body); off += 20 {
					var h SliceHash
					copy(h.MD5[:], pk.Body[off:])
					h.CRC32 = binary.LittleEndian.Uint32(pk.Body[off+16:])
					fd.Slices = append(fd.Slices, h)
				}
			case TypeRecovery:
				if len(pk.Body) < 4 {
					continue
				}
				e := binary.LittleEndian.Uint32(pk.Body)
				if _, ok := s.Recovery[e]; !ok {
					s.Recovery[e] = append([]byte(nil), pk.Body[4:]...)
				}
			case TypeCreator:
				s.Creator = string(bytes.TrimRight(pk.Body, "\x00"))
			}
		}
	}
	if !haveMain {
		return nil, errors.New("par2: no main packet found")
	}
	for _, id := range order {
		fd, ok := seen[id]
		if !ok {
			return nil, fmt.Errorf("par2: missing description for file %x", id)
		}
		s.Files = append(s.Files, fd)
	}
	return s, nil
}

// baseName is the name a file is recorded under: PAR2 stores no directory
// component for the sets this package writes.
func baseName(path string) string { return filepath.Base(path) }
