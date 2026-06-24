package sevenzip

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
	"io"
	"sync"

	"github.com/unxed/sevenzip/internal/aes7z"
	"github.com/unxed/xz/lzma"
)

// aesWriter implements a byte-stream encryptor for AES-CBC-256.
// It buffers incoming bytes and encrypts them in 16-byte blocks.
type aesWriter struct {
	w     io.Writer
	cbc   cipher.BlockMode
	buf   bytes.Buffer
	count uint64
}

func (aw *aesWriter) Write(p []byte) (int, error) {
	aw.buf.Write(p)
	for aw.buf.Len() >= 16 {
		var block [16]byte
		_, _ = aw.buf.Read(block[:])
		aw.cbc.CryptBlocks(block[:], block[:])
		if _, err := aw.w.Write(block[:]); err != nil {
			return 0, err
		}
		aw.count += 16
	}
	return len(p), nil
}

func (aw *aesWriter) Close() error {
	if aw.buf.Len() > 0 {
		pad := 16 - aw.buf.Len()
		aw.buf.Write(make([]byte, pad))
		block := aw.buf.Bytes()
		aw.cbc.CryptBlocks(block, block)
		if _, err := aw.w.Write(block); err != nil {
			return err
		}
		aw.count += 16
	}
	return nil
}
var lzmaWriterPool sync.Pool

func getLZMAWriter(w io.Writer, dictCap int, concurrency int) (*lzma.Writer2, error) {
	if v := lzmaWriterPool.Get(); v != nil {
		zw := v.(*lzma.Writer2)
		zw.Reset(w)
		return zw, nil
	}
	lzmaCfg := lzma.Writer2Config{
		DictCap:     dictCap,
		Concurrency: concurrency,
	}
	return lzmaCfg.NewWriter2(w)
}

func putLZMAWriter(zw *lzma.Writer2) {
	lzmaWriterPool.Put(zw)
}

// Новые инстанции пулеров для разного уровня параллельности будут бесшовно
// утилизироваться благодаря внутренней логике lzma.Writer2.Reset

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

// WithPassword configures the archive to be encrypted with the provided password.
func WithPassword(password string) WriterOption {
	return func(w *Writer) {
		w.password = password
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
	password   string
	closed     bool
}

type folderWriter struct {
	compressor  io.WriteCloser
	aesW        *aesWriter
	compCount   *countWriter // Physical compressed/encrypted bytes on disk
	unencComp   *countWriter // Unencrypted compressed bytes
	propByte    byte
	aesProps    []byte
	isEncrypted bool
	files       []*fileInfo
}

func (f *folderWriter) Close() error {
	if err := f.compressor.Close(); err != nil {
		return err
	}
	if zw, ok := f.compressor.(*lzma.Writer2); ok {
		putLZMAWriter(zw)
	}
	if f.isEncrypted && f.aesW != nil {
		if err := f.aesW.Close(); err != nil {
			return err
		}
	}
	return nil
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
	if n > 0 {
		_, _ = fw.crc32Hash.Write(p[:n])
		fw.uncompSize += uint64(n)
	}
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
		if err := fw.w.activeFold.Close(); err != nil {
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

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error {
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
func generateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func (w *Writer) CreateHeader(fh *FileHeader) (io.WriteCloser, error) {
	if w.closed {
		return nil, errors.New("sevenzip: writer closed")
	}
	if w.current != nil {
		if err := w.current.Close(); err != nil {
			return nil, err
		}
	}

	isDir := fh.Mode().IsDir()

	fh.isEmptyStream = isDir
	fh.isEmptyFile = false

	dictCap := 8 * 1024 * 1024 // 8 MiB dictionary
	fi := &fileInfo{
		fh:       *fh,
		propByte: lzma.EncodeDictCap(int64(dictCap)),
	}
	w.files = append(w.files, fi)

	if fh.isEmptyStream {
		w.current = &fileWriter{
			w:         w,
			fi:        fi,
			lzmaW:     &nopCloser{Writer: io.Discard},
			crc32Hash: crc32.NewIEEE(),
		}
		return w.current, nil
	}

	if w.activeFold == nil || !w.solid {
		if w.activeFold != nil {
			if err := w.activeFold.Close(); err != nil {
				return nil, err
			}
		}
		cw := &countWriter{w: w.w}

		var aesW *aesWriter
		var lzmaW io.WriteCloser
		var aesProps []byte
		var unencComp *countWriter
		var err error

		// Интеллектуальный выбор параллельности: для мелких файлов в Non-Solid зажимаем в 1 поток
		concurrency := 1
		if w.solid || fh.UncompressedSize > 512*1024 { // > 512 KB
			concurrency = 0 // 0 означает автоопределение (GOMAXPROCS) внутри lzma.Writer2Config
		}

		if w.password != "" {
			salt, err := generateRandomBytes(8)
			if err != nil {
				return nil, err
			}
			iv, err := generateRandomBytes(16)
			if err != nil {
				return nil, err
			}

			key, err := aes7z.CalculateKey(w.password, 19, salt)
			if err != nil {
				return nil, err
			}

			block, err := aes.NewCipher(key)
			if err != nil {
				return nil, err
			}

			cbc := cipher.NewCBCEncrypter(block, iv)
			aesW = &aesWriter{w: cw, cbc: cbc}

			// AES coder properties:
			// Byte 0: salt defined (0x80) | iv defined (0x40) | cycles power 19 (0x13) = 0xd3
			// Byte 1: (salt size-1 << 4) | (iv size-1) = (7 << 4) | 15 = 0x7f
			aesProps = make([]byte, 26)
			aesProps[0] = 0xd3
			aesProps[1] = 0x7f
			copy(aesProps[2:10], salt)
			copy(aesProps[10:26], iv)

			unencComp = &countWriter{w: aesW}

			lzmaW, err = getLZMAWriter(unencComp, dictCap, concurrency)
			if err != nil {
				return nil, err
			}
		} else {
			lzmaW, err = getLZMAWriter(cw, dictCap, concurrency)
			if err != nil {
				return nil, err
			}
		}

		w.activeFold = &folderWriter{
			compressor:  lzmaW,
			aesW:        aesW,
			compCount:   cw,
			unencComp:   unencComp,
			propByte:    fi.propByte,
			aesProps:    aesProps,
			isEncrypted: w.password != "",
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
		if err := w.activeFold.Close(); err != nil {
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
			var c []*coder
			var bindPairs []*bindPair
			var sizes []uint64

			var totalUncomp uint64
			for _, fi := range fold.files {
				totalUncomp += fi.uncompSize
			}

			if fold.isEncrypted {
				c_aes := &coder{
					id:         []byte{0x06, 0xf1, 0x07, 0x01}, // AES
					in:         1,
					out:        1,
					properties: fold.aesProps,
				}
				c_lzma2 := &coder{
					id:         []byte{0x21}, // LZMA2
					in:         1,
					out:        1,
					properties: []byte{fold.propByte},
				}
				c = []*coder{c_aes, c_lzma2}
				bindPairs = []*bindPair{{in: 1, out: 0}}
				sizes = []uint64{fold.unencComp.n, totalUncomp}
			} else {
				c_lzma2 := &coder{
					id:         []byte{0x21}, // LZMA2
					in:         1,
					out:        1,
					properties: []byte{fold.propByte},
				}
				c = []*coder{c_lzma2}
				sizes = []uint64{totalUncomp}
			}

			uInfo.folder[i] = &folder{
				in:            uint64(len(c)),
				out:           uint64(len(c)),
				packedStreams: 1,
				coder:         c,
				bindPair:      bindPairs,
				packed:        []uint64{0}, // packed stream is Coder 0 input (AES)
				size:          sizes,
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

	var startHdr startHeader
	var finalHeaderCRC uint32

	headerOffset, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}

	if w.password != "" {
		// Encrypt the header (Encoded Header with AES encryption)
		salt, err := generateRandomBytes(8)
		if err != nil {
			return err
		}
		iv, err := generateRandomBytes(16)
		if err != nil {
			return err
		}

		key, err := aes7z.CalculateKey(w.password, 19, salt)
		if err != nil {
			return err
		}

		block, err := aes.NewCipher(key)
		if err != nil {
			return err
		}

		cbc := cipher.NewCBCEncrypter(block, iv)
		var encBuf bytes.Buffer
		aesW := &aesWriter{w: &encBuf, cbc: cbc}
		if _, err := aesW.Write(headerBuf.Bytes()); err != nil {
			return err
		}
		if err := aesW.Close(); err != nil {
			return err
		}

		// Write encrypted header payload to the archive
		if _, err := w.w.Write(encBuf.Bytes()); err != nil {
			return err
		}

		headerPackSize := uint64(encBuf.Len())

		// Generate streamsInfo (metadata) for the Encoded Header
		pInfo := packInfo{
			position: uint64(headerOffset - 32),
			streams:  1,
			size:     []uint64{headerPackSize},
		}

		aesProps := make([]byte, 26)
		aesProps[0] = 0xd3
		aesProps[1] = 0x7f
		copy(aesProps[2:10], salt)
		copy(aesProps[10:26], iv)

		c_aes := &coder{
			id:         []byte{0x06, 0xf1, 0x07, 0x01}, // AES
			in:         1,
			out:        1,
			properties: aesProps,
		}

		uInfo := unpackInfo{
			folder: []*folder{
				{
					in:            1,
					out:           1,
					packedStreams: 1,
					coder:         []*coder{c_aes},
					packed:        []uint64{0},
					size:          []uint64{uint64(headerBuf.Len())},
				},
			},
			digest: []uint32{crc32.ChecksumIEEE(headerBuf.Bytes())},
		}

		hEnc := &streamsInfo{
			packInfo:   &pInfo,
			unpackInfo: &uInfo,
		}

		metadataOffset, err := w.w.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}

		// Encoded Header starts with idEncodedHeader (0x17)
		var metadataBuf bytes.Buffer
		metadataBuf.WriteByte(0x17)
		if err := writeStreamsInfo(&metadataBuf, hEnc); err != nil {
			return err
		}

		if _, err := w.w.Write(metadataBuf.Bytes()); err != nil {
			return err
		}

		finalHeaderCRC = crc32.ChecksumIEEE(metadataBuf.Bytes())
		startHdr = startHeader{
			Offset: uint64(metadataOffset - 32),
			Size:   uint64(metadataBuf.Len()),
			CRC:    finalHeaderCRC,
		}
	} else {
		// Non-encrypted, standard header flow
		if _, err := w.w.Write(headerBuf.Bytes()); err != nil {
			return err
		}
		finalHeaderCRC = crc32.ChecksumIEEE(headerBuf.Bytes())
		startHdr = startHeader{
			Offset: uint64(headerOffset - 32),
			Size:   uint64(headerBuf.Len()),
			CRC:    finalHeaderCRC,
		}
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