package sevenzip

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUint64RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []uint64{
		0,
		1,
		0x7f,
		0x80,
		0x3fff,
		0x4000,
		0x1fffff,
		0x200000,
		0x0fffffff,
		0x10000000,
		0x7ffffffff,
		0x800000000,
		0x3ffffffffff,
		0x40000000000,
		0x1ffffffffffff,
		0x2000000000000,
		0x0ffffffffffffff,
		0x100000000000000,
		0xffffffffffffffff,
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		err := writeUint64(&buf, tc)
		require.NoError(t, err)

		readVal, err := readUint64(&buf)
		require.NoError(t, err)

		assert.Equal(t, tc, readVal)
	}
}

func TestBoolRoundTrip(t *testing.T) {
	t.Parallel()

	tests := [][]bool{
		{true, false, true, false, true, false, true, false},
		{true, true, true},
		{false, false, false, false, false, false, false, false, true},
		make([]bool, 20), // 20 false values
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		err := writeBool(&buf, tc)
		require.NoError(t, err)

		readVal, err := readBool(&buf, uint64(len(tc)))
		require.NoError(t, err)

		assert.Equal(t, tc, readVal)
	}
}

func TestOptionalBoolRoundTrip(t *testing.T) {
	t.Parallel()

	tests := [][]bool{
		{true, true, true, true},
		{true, false, true},
		{false, false},
	}

	for _, tc := range tests {
		var buf bytes.Buffer
		err := writeOptionalBool(&buf, tc)
		require.NoError(t, err)

		readVal, err := readOptionalBool(&buf, uint64(len(tc)))
		require.NoError(t, err)

		assert.Equal(t, tc, readVal)
	}
}

func TestCRCRoundTrip(t *testing.T) {
	t.Parallel()

	crcs := []uint32{0x12345678, 0, 0x87654321}
	defined := []bool{true, false, true}

	var buf bytes.Buffer
	err := writeCRC(&buf, crcs, defined)
	require.NoError(t, err)

	readVal, err := readCRC(&buf, uint64(len(crcs)))
	require.NoError(t, err)

	// Note: readCRC returns 0 for undefined CRC spots, so we manually
	// ensure our test slice has 0 for the false boolean positions.
	assert.Equal(t, crcs, readVal)
}