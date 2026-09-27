package sevenzip

// This test verifies that the reader can decode a 7z "Encoded Header"
// (idEncodedHeader) whose single folder uses the Zstandard coder
// (0x04, 0xF7, 0x11, 0x01), i.e. an archive whose *header* itself is
// zstd-compressed, as opposed to only the file content streams.
//
// The existing Writer implementation never produces this shape (it only
// writes a plain header, or an AES-encrypted-but-uncompressed header), so
// there is no way to obtain such a fixture through the public API. Instead
// this test builds one by hand: it writes a normal, unencrypted archive
// with the Writer, then repackages the resulting plain Header as a zstd
// encoded header using the same low-level primitives writer.go already
// uses for the AES-encrypted-header case (writeStreamsInfo/coder/folder).

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// buildZstdHeaderArchive takes a plain (unencrypted, uncompressed-header) 7z
// archive produced by Writer and returns a semantically equivalent archive
// where the Header is stored as a zstd-compressed Encoded Header.
func buildZstdHeaderArchive(t *testing.T, plain []byte) []byte {
	t.Helper()

	require.GreaterOrEqual(t, len(plain), 32)

	var sh signatureHeader
	require.NoError(t, binary.Read(bytes.NewReader(plain[:32]), binary.LittleEndian, &sh))

	var start startHeader
	require.NoError(t, binary.Read(bytes.NewReader(plain[12:32]), binary.LittleEndian, &start))

	headerOffset := 32 + int64(start.Offset) //nolint:gosec
	headerEnd := headerOffset + int64(start.Size) //nolint:gosec
	require.LessOrEqual(t, headerEnd, int64(len(plain)))

	rawHeader := plain[headerOffset:headerEnd]
	require.Equal(t, byte(idHeader), rawHeader[0])

	zw, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	compressed := zw.EncodeAll(rawHeader, nil)
	require.NoError(t, zw.Close())

	// Pack stream (the zstd-compressed header) is appended right where the
	// plain header used to live.
	packOffset := start.Offset

	c := &coder{
		id:  []byte{0x04, 0xf7, 0x11, 0x01}, // Zstandard
		in:  1,
		out: 1,
	}

	f := &folder{
		in:            1,
		out:           1,
		packedStreams: 1,
		coder:         []*coder{c},
		packed:        []uint64{0},
		size:          []uint64{uint64(len(rawHeader))},
	}

	uInfo := &unpackInfo{
		folder: []*folder{f},
		digest: []uint32{crc32.ChecksumIEEE(rawHeader)},
	}

	pInfo := &packInfo{
		position: packOffset,
		streams:  1,
		size:     []uint64{uint64(len(compressed))},
	}

	si := &streamsInfo{
		packInfo:   pInfo,
		unpackInfo: uInfo,
	}

	var metadataBuf bytes.Buffer
	metadataBuf.WriteByte(idEncodedHeader)
	require.NoError(t, writeStreamsInfo(&metadataBuf, si))

	var out bytes.Buffer
	out.Write(plain[:headerOffset]) // signature header (rewritten below) + packed content streams
	out.Write(compressed)
	metadataOffset := int64(out.Len())
	out.Write(metadataBuf.Bytes())

	newStart := startHeader{
		Offset: uint64(metadataOffset - 32), //nolint:gosec
		Size:   uint64(metadataBuf.Len()),   //nolint:gosec
		CRC:    crc32.ChecksumIEEE(metadataBuf.Bytes()),
	}

	var startBuf bytes.Buffer
	require.NoError(t, binary.Write(&startBuf, binary.LittleEndian, newStart))

	newSig := signatureHeader{
		Signature: sh.Signature,
		Major:     sh.Major,
		Minor:     sh.Minor,
		CRC:       crc32.ChecksumIEEE(startBuf.Bytes()),
	}

	result := out.Bytes()

	var sigBuf bytes.Buffer
	require.NoError(t, binary.Write(&sigBuf, binary.LittleEndian, newSig))
	require.NoError(t, binary.Write(&sigBuf, binary.LittleEndian, newStart))
	copy(result[:32], sigBuf.Bytes())

	return result
}

func TestZstdEncodedHeader(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "zstdheader-*.7z")
	require.NoError(t, err)
	defer f.Close()

	w, err := NewWriter(f)
	require.NoError(t, err)

	files := map[string]string{
		"a.txt": "the quick brown fox jumps over the lazy dog, again and again",
		"b.txt": "another file, with different content, to make the header non-trivial",
	}

	for _, name := range []string{"a.txt", "b.txt"} {
		fw, err := w.Create(name)
		require.NoError(t, err)
		_, err = fw.Write([]byte(files[name]))
		require.NoError(t, err)
	}

	require.NoError(t, w.Close())

	_, err = f.Seek(0, io.SeekStart)
	require.NoError(t, err)
	plain, err := io.ReadAll(f)
	require.NoError(t, err)

	crafted := buildZstdHeaderArchive(t, plain)

	r, err := NewReader(bytes.NewReader(crafted), int64(len(crafted)))
	require.NoError(t, err)
	require.Len(t, r.File, 2)

	got := make(map[string]string, 2)

	for _, file := range r.File {
		rc, err := file.Open()
		require.NoError(t, err)

		content, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())

		got[file.Name] = string(content)
	}

	require.Equal(t, files, got)
}
