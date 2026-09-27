package sevenzip

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readRawNextHeader reopens a freshly written archive at the file-system
// level (bypassing the Reader entirely) and returns the id byte of the
// NextHeader together with the streamsInfo describing it, when it is an
// EncodedHeader. This lets tests inspect exactly what Writer.Close put on
// disk, the same way readHeader/readStreamsInfo are used by (*Reader).init.
func readRawNextHeader(t *testing.T, name string) (byte, *streamsInfo) {
	t.Helper()

	f, err := os.Open(name)
	require.NoError(t, err)
	defer f.Close()

	var sh signatureHeader
	require.NoError(t, binary.Read(f, binary.LittleEndian, &sh))

	var start startHeader
	require.NoError(t, binary.Read(f, binary.LittleEndian, &start))

	_, err = f.Seek(32+int64(start.Offset), io.SeekStart) //nolint:gosec
	require.NoError(t, err)

	br := bufio.NewReader(io.LimitReader(f, int64(start.Size))) //nolint:gosec

	id, err := br.ReadByte()
	require.NoError(t, err)

	if id != idEncodedHeader {
		return id, nil
	}

	si, err := readStreamsInfo(br)
	require.NoError(t, err)

	return id, si
}

// TestWriterHeaderIsCompressed verifies that Writer.Close, like the
// original 7-Zip archiver, does not write the archive's Header in the
// clear: it LZMA2-compresses it and wraps it in an EncodedHeader
// (idEncodedHeader / 0x17), the same coder chain real 7-Zip/p7zip expect
// and already understand when reading a .7z file (see #22).
func TestWriterHeaderIsCompressed(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp("", "sevenzip-hdrcomp-*.7z")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	w, err := NewWriter(f)
	require.NoError(t, err)

	// Enough repetitive filenames that the header has real redundancy for
	// LZMA2 to squeeze out, so a shrink in size is a meaningful signal
	// rather than noise.
	const fileCount = 200
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("some/fairly/long/repeated/directory/structure/file-%04d.txt", i)
		fw, err := w.Create(name)
		require.NoError(t, err)
		_, err = fw.Write([]byte("hello"))
		require.NoError(t, err)
	}

	require.NoError(t, w.Close())
	require.NoError(t, f.Close())

	id, si := readRawNextHeader(t, f.Name())
	require.Equal(t, idEncodedHeader, id, "NextHeader must be an EncodedHeader, not a plain Header")
	require.NotNil(t, si)
	require.NotNil(t, si.packInfo)
	require.NotNil(t, si.unpackInfo)
	require.Len(t, si.unpackInfo.folder, 1)

	folder := si.unpackInfo.folder[0]
	require.Len(t, folder.coder, 1)
	assert.Equal(t, []byte{0x21}, folder.coder[0].id, "header must be compressed with LZMA2, as original 7-Zip does")

	require.Len(t, si.packInfo.size, 1)
	packedHeaderSize := si.packInfo.size[0]
	require.NotEmpty(t, folder.size)
	uncompressedHeaderSize := folder.size[len(folder.size)-1]

	t.Logf("header: %d bytes uncompressed -> %d bytes compressed", uncompressedHeaderSize, packedHeaderSize)
	assert.Less(t, packedHeaderSize, uncompressedHeaderSize, "compressed header should be smaller than the plain one")

	// And, of course, the archive must still open and read back correctly
	// through the normal Reader, which decodes EncodedHeaders generically
	// (the same path it already uses for real-world .7z files).
	r, err := OpenReader(f.Name())
	require.NoError(t, err)
	defer r.Close()

	require.Equal(t, fileCount, len(r.File))
	for i, file := range r.File {
		assert.True(t, strings.HasSuffix(file.Name, fmt.Sprintf("file-%04d.txt", i)))
		rc, err := file.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
		require.NoError(t, rc.Close())
	}
}

// TestWriterHeaderIsCompressedAndEncrypted verifies that, when a password
// is set, the header is LZMA2-compressed *and then* AES encrypted --
// mirroring the AES(LZMA2(...)) coder chain already used for regular file
// data -- instead of only being encrypted in the clear.
func TestWriterHeaderIsCompressedAndEncrypted(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp("", "sevenzip-hdrcomp-enc-*.7z")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	w, err := NewWriter(f, WithPassword("s3cr3t"))
	require.NoError(t, err)

	const fileCount = 100
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("some/fairly/long/repeated/directory/structure/secret-%04d.txt", i)
		fw, err := w.Create(name)
		require.NoError(t, err)
		_, err = fw.Write([]byte("secret"))
		require.NoError(t, err)
	}

	require.NoError(t, w.Close())
	require.NoError(t, f.Close())

	id, si := readRawNextHeader(t, f.Name())
	require.Equal(t, idEncodedHeader, id)
	require.NotNil(t, si)
	require.Len(t, si.unpackInfo.folder, 1)

	folder := si.unpackInfo.folder[0]
	require.Len(t, folder.coder, 2, "encrypted header must go through both an AES and an LZMA2 coder")
	assert.Equal(t, []byte{0x06, 0xf1, 0x07, 0x01}, folder.coder[0].id, "first coder must be AES")
	assert.Equal(t, []byte{0x21}, folder.coder[1].id, "second coder must be LZMA2")
	require.Len(t, folder.bindPair, 1)

	r, err := OpenReaderWithPassword(f.Name(), "s3cr3t")
	require.NoError(t, err)
	defer r.Close()

	require.Equal(t, fileCount, len(r.File))
	rc, err := r.File[0].Open()
	require.NoError(t, err)
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "secret", string(data))
	require.NoError(t, rc.Close())
}
