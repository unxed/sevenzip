package sevenzip

import (
	"bytes"
	"encoding/binary"
	"time"
	"unicode/utf16"
)

func writePackInfo(w byteWriter, p *packInfo) error {
	_ = writeUint64(w, p.position)
	_ = writeUint64(w, p.streams)

	if len(p.size) > 0 {
		_ = w.WriteByte(idSize)
		_ = writeSizes(w, p.size)
	}
	if len(p.digest) > 0 {
		_ = w.WriteByte(idCRC)
		defined := make([]bool, len(p.digest))
		for i := range defined {
			defined[i] = true
		}
		if err := writeCRC(w, p.digest, defined); err != nil {
			return err
		}
	}

	_ = w.WriteByte(idEnd)
	return nil
}

func writeCoder(w byteWriter, c *coder) error {
	var flags byte
	flags |= byte(len(c.id) & 0xf)
	isComplex := c.in != 1 || c.out != 1
	if isComplex {
		flags |= 0x10
	}
	if len(c.properties) > 0 {
		flags |= 0x20
	}

	_ = w.WriteByte(flags)
	_, _ = w.Write(c.id)

	if isComplex {
		_ = writeUint64(w, c.in)
		_ = writeUint64(w, c.out)
	}
	if len(c.properties) > 0 {
		_ = writeUint64(w, uint64(len(c.properties)))
		_, _ = w.Write(c.properties)
	}
	return nil
}

func writeFolder(w byteWriter, f *folder) error {
	_ = writeUint64(w, uint64(len(f.coder)))
	for _, c := range f.coder {
		if err := writeCoder(w, c); err != nil {
			return err
		}
	}
	for _, bp := range f.bindPair {
		_ = writeUint64(w, bp.in)
		_ = writeUint64(w, bp.out)
	}
	if f.packedStreams > 1 {
		for _, p := range f.packed {
			_ = writeUint64(w, p)
		}
	}
	return nil
}

func writeUnpackInfo(w byteWriter, u *unpackInfo) error {
	_ = w.WriteByte(idFolder)
	_ = writeUint64(w, uint64(len(u.folder)))
	_ = w.WriteByte(0) // External = false

	for _, f := range u.folder {
		if err := writeFolder(w, f); err != nil {
			return err
		}
	}

	_ = w.WriteByte(idCodersUnpackSize)
	for _, f := range u.folder {
		for _, size := range f.size {
			_ = writeUint64(w, size)
		}
	}

	if len(u.digest) > 0 {
		_ = w.WriteByte(idCRC)
		defined := make([]bool, len(u.digest))
		for i := range defined {
			defined[i] = true
		}
		if err := writeCRC(w, u.digest, defined); err != nil {
			return err
		}
	}

	_ = w.WriteByte(idEnd)
	return nil
}

func writeSubStreamsInfo(w byteWriter, s *subStreamsInfo, folders []*folder) error {
	if len(s.streams) > 0 {
		allOne := true
		for _, count := range s.streams {
			if count != 1 {
				allOne = false
				break
			}
		}
		if !allOne {
			_ = w.WriteByte(idNumUnpackStream)
			for _, count := range s.streams {
				_ = writeUint64(w, count)
			}
		}
	}

	if len(s.size) > 0 {
		hasSize := false
		for _, count := range s.streams {
			if count > 1 {
				hasSize = true
				break
			}
		}
		if hasSize {
			_ = w.WriteByte(idSize)
			k := 0
			for _, count := range s.streams {
				if count == 0 {
					continue
				}
				for j := uint64(1); j < count; j++ {
					_ = writeUint64(w, s.size[k])
					k++
				}
				k++ // skip the last one in folder
			}
		}
	}

	if len(s.digest) > 0 {
		_ = w.WriteByte(idCRC)
		defined := make([]bool, len(s.digest))
		for i := range defined {
			defined[i] = true
		}
		if err := writeCRC(w, s.digest, defined); err != nil {
			return err
		}
	}

	_ = w.WriteByte(idEnd)
	return nil
}

func writeStreamsInfo(w byteWriter, s *streamsInfo) error {
	if s.packInfo != nil {
		_ = w.WriteByte(idPackInfo)
		if err := writePackInfo(w, s.packInfo); err != nil {
			return err
		}
	}
	if s.unpackInfo != nil {
		_ = w.WriteByte(idUnpackInfo)
		if err := writeUnpackInfo(w, s.unpackInfo); err != nil {
			return err
		}
	}
	if s.subStreamsInfo != nil {
		_ = w.WriteByte(idSubStreamsInfo)
		if err := writeSubStreamsInfo(w, s.subStreamsInfo, s.unpackInfo.folder); err != nil {
			return err
		}
	}
	_ = w.WriteByte(idEnd)
	return nil
}

func timeToFiletime(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	// Convert Unix time to 100-nanosecond intervals since Jan 1, 1601 (UTC)
	return uint64(t.Unix()*10000000) + uint64(t.Nanosecond()/100) + 116444736000000000
}

func hasTime(f *filesInfo, get func(*FileHeader) time.Time) bool {
	for _, fh := range f.file {
		if !get(&fh).IsZero() {
			return true
		}
	}
	return false
}

func writeTimeProp(w byteWriter, id byte, f *filesInfo, get func(*FileHeader) time.Time) error {
	var timeBuf bytes.Buffer
	var defined []bool
	for _, fh := range f.file {
		defined = append(defined, !get(&fh).IsZero())
	}
	_ = writeOptionalBool(&timeBuf, defined)
	timeBuf.WriteByte(0) // External

	var b [8]byte
	for _, fh := range f.file {
		t := get(&fh)
		if !t.IsZero() {
			binary.LittleEndian.PutUint64(b[:], timeToFiletime(t))
			timeBuf.Write(b[:])
		}
	}

	_ = w.WriteByte(id)
	_ = writeUint64(w, uint64(timeBuf.Len()))
	_, _ = w.Write(timeBuf.Bytes())
	return nil
}

func writeFilesInfo(w byteWriter, f *filesInfo) error {
	_ = writeUint64(w, uint64(len(f.file)))

	var emptyStreams []bool
	hasEmptyStream := false
	for _, fh := range f.file {
		emptyStreams = append(emptyStreams, fh.isEmptyStream)
		if fh.isEmptyStream {
			hasEmptyStream = true
		}
	}

	if hasEmptyStream {
		_ = w.WriteByte(idEmptyStream)
		size := (len(emptyStreams) + 7) / 8
		_ = writeUint64(w, uint64(size))
		_ = writeBool(w, emptyStreams)

		var emptyFiles []bool
		hasEmptyFile := false
		for _, fh := range f.file {
			if fh.isEmptyStream {
				emptyFiles = append(emptyFiles, fh.isEmptyFile)
				if fh.isEmptyFile {
					hasEmptyFile = true
				}
			}
		}
		if hasEmptyFile {
			_ = w.WriteByte(idEmptyFile)
			size := (len(emptyFiles) + 7) / 8
			_ = writeUint64(w, uint64(size))
			_ = writeBool(w, emptyFiles)
		}

		var antiFiles []bool
		// Assume no anti-files generated directly for now, keeping simple
		for _, fh := range f.file {
			if fh.isEmptyStream {
				antiFiles = append(antiFiles, false)
			}
		}
		// In a full implementation we'd check for anti-files here.
	}

	if hasTime(f, func(fh *FileHeader) time.Time { return fh.Created }) {
		_ = writeTimeProp(w, idCTime, f, func(fh *FileHeader) time.Time { return fh.Created })
	}
	if hasTime(f, func(fh *FileHeader) time.Time { return fh.Accessed }) {
		_ = writeTimeProp(w, idATime, f, func(fh *FileHeader) time.Time { return fh.Accessed })
	}
	if hasTime(f, func(fh *FileHeader) time.Time { return fh.Modified }) {
		_ = writeTimeProp(w, idMTime, f, func(fh *FileHeader) time.Time { return fh.Modified })
	}

	var namesBuf bytes.Buffer
	namesBuf.WriteByte(0) // External
	for _, fh := range f.file {
		encoded := utf16.Encode([]rune(fh.Name))
		for _, r := range encoded {
			namesBuf.WriteByte(byte(r))
			namesBuf.WriteByte(byte(r >> 8))
		}
		namesBuf.WriteByte(0)
		namesBuf.WriteByte(0)
	}
	_ = w.WriteByte(idName)
	_ = writeUint64(w, uint64(namesBuf.Len()))
	_, _ = w.Write(namesBuf.Bytes())

	hasAttr := false
	for _, fh := range f.file {
		if fh.Attributes != 0 {
			hasAttr = true
			break
		}
	}
	if hasAttr {
		var attrBuf bytes.Buffer
		var defined []bool
		for _, fh := range f.file {
			defined = append(defined, fh.Attributes != 0)
		}
		_ = writeOptionalBool(&attrBuf, defined)
		attrBuf.WriteByte(0) // External
		var b [4]byte
		for _, fh := range f.file {
			if fh.Attributes != 0 {
				binary.LittleEndian.PutUint32(b[:], fh.Attributes)
				attrBuf.Write(b[:])
			}
		}
		_ = w.WriteByte(idWinAttributes)
		_ = writeUint64(w, uint64(attrBuf.Len()))
		_, _ = w.Write(attrBuf.Bytes())
	}

	_ = w.WriteByte(idEnd)
	return nil
}

func writeHeader(w byteWriter, h *header) error {
	// Note: readHeader (parser) starts after idHeader has been consumed.
	// It expects properties in a specific sequence.
	if h.streamsInfo != nil {
		_ = w.WriteByte(idMainStreamsInfo)
		if err := writeStreamsInfo(w, h.streamsInfo); err != nil {
			return err
		}
	}
	if h.filesInfo != nil {
		_ = w.WriteByte(idFilesInfo)
		if err := writeFilesInfo(w, h.filesInfo); err != nil {
			return err
		}
	}
	_ = w.WriteByte(idEnd)
	return nil
}