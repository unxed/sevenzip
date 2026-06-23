package sevenzip

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderRoundTrip(t *testing.T) {
	t.Parallel()

	h := &header{
		streamsInfo: &streamsInfo{
			packInfo: &packInfo{
				position: 100,
				streams:  1,
				size:     []uint64{200},
				digest:   []uint32{0x12345678},
			},
			unpackInfo: &unpackInfo{
				folder: []*folder{
					{
						in: 1, out: 1, packedStreams: 1,
						coder: []*coder{
							{
								id:         []byte{0x21}, // LZMA2
								in:         1,
								out:        1,
								properties: []byte{0x01},
							},
						},
						bindPair: []*bindPair{},
						packed:   []uint64{0}, // Derived dynamically usually, but safe to set empty/defaults
						size:     []uint64{500},
					},
				},
				digest: []uint32{0x87654321},
			},
			subStreamsInfo: &subStreamsInfo{
				streams: []uint64{2},
				size:    []uint64{300, 200}, // the second file is 500-300=200
				digest:  []uint32{0x11111111, 0x22222222},
			},
		},
		filesInfo: &filesInfo{
			file: []FileHeader{
				{
					Name:             "file1.txt",
					Modified:         time.Unix(1000000000, 0).UTC(),
					Attributes:       0x20, // Archive
					UncompressedSize: 300,
					CRC32:            0x11111111,
				},
				{
					Name:             "file2.txt",
					Modified:         time.Unix(1100000000, 0).UTC(),
					Attributes:       0x20,
					UncompressedSize: 200,
					CRC32:            0x22222222,
				},
			},
		},
	}

	var buf bytes.Buffer
	err := writeHeader(&buf, h)
	require.NoError(t, err)

	reader := bytes.NewReader(buf.Bytes())
	parsedHeader, err := readHeader(reader)
	require.NoError(t, err)

	assert.NotNil(t, parsedHeader.streamsInfo)
	assert.NotNil(t, parsedHeader.filesInfo)
	assert.Equal(t, 2, len(parsedHeader.filesInfo.file))

	assert.Equal(t, "file1.txt", parsedHeader.filesInfo.file[0].Name)
	assert.Equal(t, uint32(0x20), parsedHeader.filesInfo.file[0].Attributes)
	assert.Equal(t, time.Unix(1000000000, 0).UTC(), parsedHeader.filesInfo.file[0].Modified)

	assert.Equal(t, "file2.txt", parsedHeader.filesInfo.file[1].Name)

	assert.Equal(t, uint64(200), parsedHeader.streamsInfo.packInfo.size[0])
	assert.Equal(t, uint32(0x12345678), parsedHeader.streamsInfo.packInfo.digest[0])

	assert.Equal(t, []byte{0x21}, parsedHeader.streamsInfo.unpackInfo.folder[0].coder[0].id)
	assert.Equal(t, uint64(500), parsedHeader.streamsInfo.unpackInfo.folder[0].size[0])
}