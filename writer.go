package sevenzip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
	"io"

	"github.com/unxed/xz/lzma"
)

// WriterOption is a functional option for configuring a Writer.
type WriterOption func(*Writer)

// WithSolid enables or disables solid compression mode.
// In solid mode, multiple files are compressed together in a single stream,
// significantly improving compression ratio for many small files.
func WithSolid(solid bool) WriterOption {
	return func(w *Writer) {
		w.solid = solid
	}
}

// Writer provides an API for creating 7-zip archives.
type Writer struct {
	w          io.WriteSeeker
	files      []*fileInfo
	folders    []*folderWriter
	current    *fileWriter
	activeFold *folderWriter
	solid      bool
	closed     bool
}

type folderWriter struct {
	compressor io.WriteCloser
	compCount  *countWriter
	propByte   byte
	files      []*fileInfo
}

type fileInfo struct {
	fh         FileHeader
	propByte   byte
	compSize   uint64
	uncompSize uint64
	crc32      uint32
}

// countWriter wraps an io.Writer to track the number of bytes written.
type countWriter struct {
	w io.Writer
	n uint64
}

func (cw *countWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.n += uint64(n)
	return n, err
}

type fileWriter struct {
	w          *Writer
	fi         *fileInfo
	lzmaW      io.Writer
	crc32Hash  hash.Hash32
	uncompSize uint64
	closed     bool
}

func (fw *fileWriter) Write(p []byte) (int, error) {
	if fw.closed {
		return 0, errors.New("sevenzip: file writer already closed")
	}
	n, err := fw.lzmaW.Write(p)
	_, _ = fw.crc32Hash.Write(p[:n])
	fw.uncompSize += uint64(n)
	return n, err
}

func (fw *fileWriter) Close() error {
	if fw.closed {
		return nil
	}
	fw.closed = true

	// If we are NOT in solid mode, this file has its own exclusive compressor stream
	// that must be closed right now so its LZMA2 EOS marker and buffers are flushed.
	if !fw.w.solid && fw.w.activeFold != nil {
		if err := fw.w.activeFold.compressor.Close(); err != nil {
			return err
		}
		fw.w.activeFold = nil
	}

	fw.fi.uncompSize = fw.uncompSize
	fw.fi.crc32 = fw.crc32Hash.Sum32()

	// Update original header with final sizes and checksums
	fw.fi.fh.UncompressedSize = fw.fi.uncompSize
	fw.fi.fh.CRC32 = fw.fi.crc32

	fw.w.current = nil
	return nil
}

// NewWriter returns a new Writer writing a 7-zip archive to w.
// The provided io.WriteSeeker must allow seeking back to the start
// to write the final signature header.
func NewWriter(w io.WriteSeeker, opts ...WriterOption) (*Writer, error) {
	// Reserve 32 bytes for the SignatureHeader and StartHeader
	if _, err := w.Seek(32, io.SeekStart); err != nil {
		return nil, err
	}
	wr := &Writer{w: w}
	for _, opt := range opts {
		opt(wr)
	}
	return wr, nil
}

// Create adds a file to the archive using the provided name.
// It returns a Writer to which the file contents should be written.
// The file contents will be compressed using LZMA2.
func (w *Writer) Create(name string) (io.WriteCloser, error) {
	return w.CreateHeader(&FileHeader{Name: name})
}

// CreateHeader adds a file to the archive using the provided FileHeader.
// It returns a Writer to which the file contents should be written.
// If another file is currently being written, it will be implicitly closed.
func (w *Writer) CreateHeader(fh *FileHeader) (io.WriteCloser, error) {
	if w.closed {
		return nil, errors.New("sevenzip: writer closed")
	}
	if w.current != nil {
		if err := w.current.Close(); err != nil {
			return nil, err
		}
	}

	dictCap := 8 * 1024 * 1024 // 8 MiB dictionary
	fi := &fileInfo{
		fh:       *fh,
		propByte: lzma.EncodeDictCap(int64(dictCap)),
	}
	w.files = append(w.files, fi)

	if w.activeFold == nil || !w.solid {
		if w.activeFold != nil {
			// In non-solid mode, activeFold is closed in fileWriter.Close,
			// but if for some reason it wasn't, close it here.
			if err := w.activeFold.compressor.Close(); err != nil {
				return nil, err
			}
		}
		cw := &countWriter{w: w.w}
		lzmaCfg := lzma.Writer2Config{DictCap: dictCap}
		lzmaW, err := lzmaCfg.NewWriter2(cw)
		if err != nil {
			return nil, err
		}
		w.activeFold = &folderWriter{
			compressor: lzmaW,
			compCount:  cw,
			propByte:   fi.propByte,
		}
		w.folders = append(w.folders, w.activeFold)
	}

	w.activeFold.files = append(w.activeFold.files, fi)

	w.current = &fileWriter{
		w:         w,
		fi:        fi,
		lzmaW:     w.activeFold.compressor,
		crc32Hash: crc32.NewIEEE(),
	}
	return w.current, nil
}

// Close finishes writing the 7-zip archive.
// It writes the directory metadata and updates the signature header.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.current != nil {
		if err := w.current.Close(); err != nil {
			return err
		}
	}
	if w.activeFold != nil {
		if err := w.activeFold.compressor.Close(); err != nil {
			return err
		}
		w.activeFold = nil
	}

	h := &header{}

	if len(w.files) > 0 {
		var pInfo packInfo
		var uInfo unpackInfo
		var fInfo filesInfo

		pInfo.position = 0
		pInfo.streams = uint64(len(w.folders))
		for _, fold := range w.folders {
			pInfo.size = append(pInfo.size, fold.compCount.n)
		}

		uInfo.folder = make([]*folder, len(w.folders))
		for i, fold := range w.folders {
			c := &coder{
				id:         []byte{0x21}, // LZMA2
				in:         1,
				out:        1,
				properties: []byte{fold.propByte},
			}
			var totalUncomp uint64
			for _, fi := range fold.files {
				totalUncomp += fi.uncompSize
			}
			uInfo.folder[i] = &folder{
				in:            1,
				out:           1,
				packedStreams: 1,
				coder:         []*coder{c},
				packed:        []uint64{0}, // 1 input stream, 0th index
				size:          []uint64{totalUncomp},
			}
		}

		// We always generate subStreamsInfo. This guarantees that parsers
		// (including the underlying reader in this package) can accurately
		// map individual files to their respective folders, preventing
		// out-of-bounds seeks in non-solid mode.
		needsSubStreams := true

		if needsSubStreams {
			sInfo := &subStreamsInfo{}
			for _, fold := range w.folders {
				sInfo.streams = append(sInfo.streams, uint64(len(fold.files)))
				for _, fi := range fold.files {
					sInfo.size = append(sInfo.size, fi.uncompSize)
					sInfo.digest = append(sInfo.digest, fi.crc32)
				}
			}
			h.streamsInfo = &streamsInfo{
				packInfo:       &pInfo,
				unpackInfo:     &uInfo,
				subStreamsInfo: sInfo,
			}
		} else {
			for _, fold := range w.folders {
				uInfo.digest = append(uInfo.digest, fold.files[0].crc32)
			}
			h.streamsInfo = &streamsInfo{
				packInfo:   &pInfo,
				unpackInfo: &uInfo,
			}
		}

		for _, fi := range w.files {
			fInfo.file = append(fInfo.file, fi.fh)
		}

		h.filesInfo = &fInfo
	}

	var headerBuf bytes.Buffer
	headerBuf.WriteByte(0x01) // idHeader
	if err := writeHeader(&headerBuf, h); err != nil {
		return err
	}

	headerOffset, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}

	if _, err := w.w.Write(headerBuf.Bytes()); err != nil {
		return err
	}

	headerCRC := crc32.ChecksumIEEE(headerBuf.Bytes())

	startHdr := startHeader{
		Offset: uint64(headerOffset - 32),
		Size:   uint64(headerBuf.Len()),
		CRC:    headerCRC,
	}

	var startBuf bytes.Buffer
	_ = binary.Write(&startBuf, binary.LittleEndian, startHdr.Offset)
	_ = binary.Write(&startBuf, binary.LittleEndian, startHdr.Size)
	_ = binary.Write(&startBuf, binary.LittleEndian, startHdr.CRC)

	startCRC := crc32.ChecksumIEEE(startBuf.Bytes())

	sigHdr := signatureHeader{
		Signature: [6]byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C},
		Major:     0,
		Minor:     4,
		CRC:       startCRC,
	}

	if _, err := w.w.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(w.w, binary.LittleEndian, sigHdr); err != nil {
		return err
	}
	if err := binary.Write(w.w, binary.LittleEndian, startHdr); err != nil {
		return err
	}

	return nil
}