package render

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// gopdf does not understand TrueType Collection (.ttc) files: AddTTFFont
// reads the first 4 bytes expecting an sfnt version and silently rejects
// "ttcf"-tagged collections. macOS (PingFang.ttc, Helvetica.ttc) and most
// Linux Noto-CJK packages ship CJK fonts as TTCs, so without our own
// extraction step the fallback face would silently never load.
//
// extractTTCFace0 reads a TTC file and returns the bytes of its first
// face (font 0) as a standalone TTF, suitable for AddTTFFontData. The
// TTC format is documented in OpenType spec §"Font Collections":
//
//	TTCHeader v1/v2
//	  uint32 ttcTag        ("ttcf")
//	  uint32 majorVersion  (1 or 2)
//	  uint32 minorVersion
//	  uint32 numFonts
//	  uint32 offsetTable[numFonts] -- absolute file offset of each face's
//	                                  sfnt offset table
//	  (v2 only) uint32 dsigTag / dsigLength / dsigOffset
//
//	Each offsetTable entry points to a normal sfnt:
//	  uint32 sfntVersion
//	  uint16 numTables, searchRange, entrySelector, rangeShift
//	  TableDirEntry[numTables]
//	    uint32 tag, checksum, offset, length
//
// Table data live at absolute offsets in the TTC file. To produce a
// standalone TTF we rewrite the offsets to be relative to the new
// file's start and append the table payloads contiguously.
func extractTTCFace0(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return extractTTCFace0Bytes(data)
}

func extractTTCFace0Bytes(data []byte) ([]byte, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("ttc: too short (%d bytes)", len(data))
	}
	if string(data[0:4]) != "ttcf" {
		return nil, fmt.Errorf("ttc: bad signature %q", data[0:4])
	}
	numFonts := binary.BigEndian.Uint32(data[8:12])
	if numFonts == 0 {
		return nil, fmt.Errorf("ttc: zero fonts in collection")
	}
	// numFonts × 4 bytes of offsets start at byte 12.
	if len(data) < 12+4 {
		return nil, fmt.Errorf("ttc: missing offset table")
	}
	face0Offset := binary.BigEndian.Uint32(data[12:16])
	if int(face0Offset)+12 > len(data) {
		return nil, fmt.Errorf("ttc: face 0 offset %d out of range", face0Offset)
	}

	// Read face 0's sfnt offset table.
	sfntStart := int(face0Offset)
	sfntVersion := binary.BigEndian.Uint32(data[sfntStart:])
	// gopdf only handles version 0x00010000 (TrueType outlines).
	// "OTTO" (0x4F54544F) is OpenType with CFF/PostScript outlines —
	// detect that up front so the caller sees a useful message rather
	// than gopdf's generic "Unrecognized file (font) format".
	if sfntVersion == 0x4F54544F {
		return nil, fmt.Errorf(
			"ttc: face 0 uses OpenType/CFF outlines (sfnt %q); gopdf only renders TrueType — "+
				"use a TrueType CJK font like WenQuanYi Zen Hei",
			"OTTO")
	}
	if sfntVersion != 0x00010000 {
		return nil, fmt.Errorf("ttc: face 0 has unsupported sfnt version 0x%08X", sfntVersion)
	}
	numTables := binary.BigEndian.Uint16(data[sfntStart+4:])
	searchRange := binary.BigEndian.Uint16(data[sfntStart+6:])
	entrySelector := binary.BigEndian.Uint16(data[sfntStart+8:])
	rangeShift := binary.BigEndian.Uint16(data[sfntStart+10:])

	dirStart := sfntStart + 12
	if dirStart+int(numTables)*16 > len(data) {
		return nil, fmt.Errorf("ttc: face 0 table directory out of range")
	}

	type entry struct {
		tag      uint32
		checksum uint32
		offset   uint32 // absolute in TTC
		length   uint32
	}
	entries := make([]entry, numTables)
	for i := 0; i < int(numTables); i++ {
		o := dirStart + i*16
		entries[i] = entry{
			tag:      binary.BigEndian.Uint32(data[o:]),
			checksum: binary.BigEndian.Uint32(data[o+4:]),
			offset:   binary.BigEndian.Uint32(data[o+8:]),
			length:   binary.BigEndian.Uint32(data[o+12:]),
		}
	}

	// Compose new TTF. Offsets in the rewritten directory are relative
	// to the new file's start. Tables are 4-byte aligned per spec.
	headerSize := 12 + len(entries)*16
	newOffsets := make([]uint32, len(entries))
	totalSize := headerSize
	for i := range entries {
		newOffsets[i] = uint32(totalSize)
		totalSize += int(entries[i].length)
		if pad := (4 - totalSize%4) % 4; pad != 0 {
			totalSize += pad
		}
	}

	out := make([]byte, totalSize)
	// sfnt offset table
	binary.BigEndian.PutUint32(out[0:], sfntVersion)
	binary.BigEndian.PutUint16(out[4:], numTables)
	binary.BigEndian.PutUint16(out[6:], searchRange)
	binary.BigEndian.PutUint16(out[8:], entrySelector)
	binary.BigEndian.PutUint16(out[10:], rangeShift)
	// table directory with rewritten offsets
	for i, e := range entries {
		o := 12 + i*16
		binary.BigEndian.PutUint32(out[o:], e.tag)
		binary.BigEndian.PutUint32(out[o+4:], e.checksum)
		binary.BigEndian.PutUint32(out[o+8:], newOffsets[i])
		binary.BigEndian.PutUint32(out[o+12:], e.length)
	}
	// copy each table's bytes
	for i, e := range entries {
		src := data[e.offset : e.offset+e.length]
		copy(out[newOffsets[i]:], src)
	}
	return out, nil
}

// looksLikeTTC reports whether the file at path starts with the "ttcf"
// signature. We check the first 4 bytes only — cheap, no full load.
// Errors are conservatively treated as "not a TTC" so callers fall
// through to the plain AddTTFFont path with their original error.
func looksLikeTTC(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return string(hdr[:]) == "ttcf"
}
