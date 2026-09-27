package util

import "testing"

func TestCRC32Equal(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		c    uint32
		want bool
	}{
		{
			name: "matching checksum",
			b:    []byte{0x12, 0x34, 0x56, 0x78},
			c:    0x12345678,
			want: true,
		},
		{
			name: "mismatched checksum",
			b:    []byte{0x00, 0x00, 0x00, 0x00},
			c:    0x12345678,
			want: false,
		},
		{
			name: "wrong length",
			b:    []byte{0x12, 0x34, 0x56},
			c:    0x12345678,
			want: false,
		},
		{
			name: "zero checksum against zero bytes",
			b:    []byte{0x00, 0x00, 0x00, 0x00},
			c:    0,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CRC32Equal(tt.b, tt.c); got != tt.want {
				t.Errorf("CRC32Equal(%v, %#x) = %v, want %v", tt.b, tt.c, got, tt.want)
			}
		})
	}
}
