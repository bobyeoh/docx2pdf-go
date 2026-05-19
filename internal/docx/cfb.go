package docx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"
)

// cfb.go implements just enough Compound File Binary (MS-CFB) reader to
// extract two streams Word writes into password-protected .docx files:
//
//   - "EncryptionInfo"       — the cipher header + verifier
//   - "EncryptedPackage"     — the actual encrypted .docx zip blob
//
// References: [MS-CFB] 2.x. We support the v3 sector size (512 bytes);
// v4 (4096) and DIFAT-overflow streams are out of scope. The DIFAT
// chain handling supports the in-header 109 entries only — large
// containers (DIFAT spilled past the header) are not parsed.
//
// Reader concurrency: cfbReader is single-shot, not safe for parallel use.

const (
	cfbSectorSize   = 512
	cfbMiniSectorSz = 64
	cfbDirEntrySize = 128
	cfbEndOfChain   = 0xFFFFFFFE
	cfbFreeSect     = 0xFFFFFFFF
)

type cfbReader struct {
	data    []byte
	sectSz  int
	fat     []uint32 // sector allocation table
	miniFat []uint32
	mini    []byte // mini-stream backing buffer
	dirs    []cfbDirEntry
}

type cfbDirEntry struct {
	name      string
	objType   byte // 1=storage 2=stream 5=root
	startSect uint32
	size      uint64
}

// openCFB parses a CFB v3 file into directory + FAT tables. Returns an
// error when the file isn't recognizable as CFB.
func openCFB(data []byte) (*cfbReader, error) {
	if len(data) < 512 {
		return nil, errors.New("cfb: too short for header")
	}
	if !(data[0] == 0xD0 && data[1] == 0xCF && data[2] == 0x11 && data[3] == 0xE0 &&
		data[4] == 0xA1 && data[5] == 0xB1 && data[6] == 0x1A && data[7] == 0xE1) {
		return nil, errors.New("cfb: bad signature")
	}
	majorVer := binary.LittleEndian.Uint16(data[26:])
	sectorShift := binary.LittleEndian.Uint16(data[30:])
	sectSz := 1 << sectorShift
	if majorVer == 3 {
		sectSz = 512
	} else if majorVer == 4 {
		sectSz = 4096
	}
	r := &cfbReader{data: data, sectSz: sectSz}
	numFAT := int(binary.LittleEndian.Uint32(data[44:]))
	firstDirSect := binary.LittleEndian.Uint32(data[48:])
	miniCutoff := binary.LittleEndian.Uint32(data[56:])
	firstMiniFAT := binary.LittleEndian.Uint32(data[60:])
	// 109 FAT sector indexes start at offset 76 in the header.
	difat := make([]uint32, 0, numFAT)
	for i := 0; i < 109 && i < numFAT; i++ {
		difat = append(difat, binary.LittleEndian.Uint32(data[76+i*4:]))
	}
	// Build FAT.
	for _, fs := range difat {
		off := int(fs+1) * sectSz
		end := off + sectSz
		if end > len(data) {
			break
		}
		for i := 0; i+4 <= sectSz; i += 4 {
			r.fat = append(r.fat, binary.LittleEndian.Uint32(data[off+i:]))
		}
	}
	// Walk directory chain.
	for sect := firstDirSect; sect != cfbEndOfChain && sect != cfbFreeSect; {
		off := int(sect+1) * sectSz
		if off+sectSz > len(data) {
			break
		}
		for j := 0; j+cfbDirEntrySize <= sectSz; j += cfbDirEntrySize {
			ent := parseCFBDirEntry(data[off+j : off+j+cfbDirEntrySize])
			if ent.objType != 0 {
				r.dirs = append(r.dirs, ent)
			}
		}
		if int(sect) >= len(r.fat) {
			break
		}
		sect = r.fat[sect]
	}
	// Mini FAT.
	for sect := firstMiniFAT; sect != cfbEndOfChain && sect != cfbFreeSect; {
		off := int(sect+1) * sectSz
		if off+sectSz > len(data) {
			break
		}
		for i := 0; i+4 <= sectSz; i += 4 {
			r.miniFat = append(r.miniFat, binary.LittleEndian.Uint32(data[off+i:]))
		}
		if int(sect) >= len(r.fat) {
			break
		}
		sect = r.fat[sect]
	}
	// Mini-stream content (held in the root entry's stream).
	if len(r.dirs) > 0 && r.dirs[0].objType == 5 && r.dirs[0].size > 0 {
		r.mini = r.readStreamFAT(r.dirs[0].startSect, int(r.dirs[0].size))
	}
	_ = miniCutoff
	return r, nil
}

// findStream returns the directory entry for `name` or false.
func (r *cfbReader) findStream(name string) (cfbDirEntry, bool) {
	for _, d := range r.dirs {
		if d.name == name && d.objType == 2 {
			return d, true
		}
	}
	return cfbDirEntry{}, false
}

// readStream pulls a stream by name. Uses the regular FAT for sizes
// above the mini-stream cutoff (4 KB), and the mini-FAT otherwise.
func (r *cfbReader) readStream(name string) ([]byte, bool) {
	d, ok := r.findStream(name)
	if !ok {
		return nil, false
	}
	if d.size < 4096 && len(r.mini) > 0 {
		return r.readMiniStream(d.startSect, int(d.size)), true
	}
	return r.readStreamFAT(d.startSect, int(d.size)), true
}

func (r *cfbReader) readStreamFAT(start uint32, size int) []byte {
	out := make([]byte, 0, size)
	for sect := start; sect != cfbEndOfChain && sect != cfbFreeSect; {
		off := int(sect+1) * r.sectSz
		if off+r.sectSz > len(r.data) {
			break
		}
		out = append(out, r.data[off:off+r.sectSz]...)
		if len(out) >= size {
			break
		}
		if int(sect) >= len(r.fat) {
			break
		}
		sect = r.fat[sect]
	}
	if len(out) > size {
		out = out[:size]
	}
	return out
}

func (r *cfbReader) readMiniStream(start uint32, size int) []byte {
	out := make([]byte, 0, size)
	for sect := start; sect != cfbEndOfChain && sect != cfbFreeSect; {
		off := int(sect) * cfbMiniSectorSz
		if off+cfbMiniSectorSz > len(r.mini) {
			break
		}
		out = append(out, r.mini[off:off+cfbMiniSectorSz]...)
		if len(out) >= size {
			break
		}
		if int(sect) >= len(r.miniFat) {
			break
		}
		sect = r.miniFat[sect]
	}
	if len(out) > size {
		out = out[:size]
	}
	return out
}

func parseCFBDirEntry(b []byte) cfbDirEntry {
	nameLen := int(binary.LittleEndian.Uint16(b[64:]))
	if nameLen > 0 {
		nameLen -= 2 // null terminator counted
	}
	if nameLen < 0 {
		nameLen = 0
	}
	if nameLen > 64 {
		nameLen = 64
	}
	us := make([]uint16, nameLen/2)
	for i := range us {
		us[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return cfbDirEntry{
		name:      string(utf16.Decode(us)),
		objType:   b[66],
		startSect: binary.LittleEndian.Uint32(b[116:]),
		size:      binary.LittleEndian.Uint64(b[120:]),
	}
}

// readAllToBytes reads everything from r into a byte slice. Helper used
// by Open/Parse to load the entire input when we suspect CFB.
func readAllToBytes(r io.ReaderAt, size int64) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("invalid size %d", size)
	}
	buf := make([]byte, size)
	_, err := r.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}
