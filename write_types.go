package sevenzip

import (
	"encoding/binary"
	"io"
)

// byteWriter is an interface combining io.Writer and io.ByteWriter
type byteWriter interface {
	io.Writer
	io.ByteWriter
}

// writeUint64 writes an unsigned 64-bit integer using 7z's variable length encoding.
// It is the exact inverse of readUint64 from types.go.
func writeUint64(w io.ByteWriter, v uint64) error {
	if v < 0x80 {
		return w.WriteByte(byte(v))
	} else if v < 0x4000 {
		_ = w.WriteByte(byte(v>>8) | 0x80)
		return w.WriteByte(byte(v))
	} else if v < 0x200000 {
		_ = w.WriteByte(byte(v>>16) | 0xc0)
		_ = w.WriteByte(byte(v))
		return w.WriteByte(byte(v >> 8))
	} else if v < 0x10000000 {
		_ = w.WriteByte(byte(v>>24) | 0xe0)
		_ = w.WriteByte(byte(v))
		_ = w.WriteByte(byte(v >> 8))
		return w.WriteByte(byte(v >> 16))
	} else if v < 0x800000000 {
		_ = w.WriteByte(byte(v>>32) | 0xf0)
		_ = w.WriteByte(byte(v))
		_ = w.WriteByte(byte(v >> 8))
		_ = w.WriteByte(byte(v >> 16))
		return w.WriteByte(byte(v >> 24))
	} else if v < 0x40000000000 {
		_ = w.WriteByte(byte(v>>40) | 0xf8)
		_ = w.WriteByte(byte(v))
		_ = w.WriteByte(byte(v >> 8))
		_ = w.WriteByte(byte(v >> 16))
		_ = w.WriteByte(byte(v >> 24))
		return w.WriteByte(byte(v >> 32))
	} else if v < 0x2000000000000 {
		_ = w.WriteByte(byte(v>>48) | 0xfc)
		_ = w.WriteByte(byte(v))
		_ = w.WriteByte(byte(v >> 8))
		_ = w.WriteByte(byte(v >> 16))
		_ = w.WriteByte(byte(v >> 24))
		_ = w.WriteByte(byte(v >> 32))
		return w.WriteByte(byte(v >> 40))
	} else if v < 0x100000000000000 {
		_ = w.WriteByte(byte(v>>56) | 0xfe)
		_ = w.WriteByte(byte(v))
		_ = w.WriteByte(byte(v >> 8))
		_ = w.WriteByte(byte(v >> 16))
		_ = w.WriteByte(byte(v >> 24))
		_ = w.WriteByte(byte(v >> 32))
		_ = w.WriteByte(byte(v >> 40))
		return w.WriteByte(byte(v >> 48))
	}

	_ = w.WriteByte(0xff)
	_ = w.WriteByte(byte(v))
	_ = w.WriteByte(byte(v >> 8))
	_ = w.WriteByte(byte(v >> 16))
	_ = w.WriteByte(byte(v >> 24))
	_ = w.WriteByte(byte(v >> 32))
	_ = w.WriteByte(byte(v >> 40))
	_ = w.WriteByte(byte(v >> 48))
	return w.WriteByte(byte(v >> 56))
}

// writeBool writes a slice of booleans, packing 8 booleans into each byte.
func writeBool(w io.ByteWriter, defined []bool) error {
	var b byte
	var mask byte = 0x80
	for _, def := range defined {
		if def {
			b |= mask
		}
		mask >>= 1
		if mask == 0 {
			if err := w.WriteByte(b); err != nil {
				return err
			}
			b = 0
			mask = 0x80
		}
	}
	if mask != 0x80 {
		if err := w.WriteByte(b); err != nil {
			return err
		}
	}
	return nil
}

// writeOptionalBool writes a 1 if all bools are true. Otherwise, it writes 0
// followed by the packed boolean array.
func writeOptionalBool(w io.ByteWriter, defined []bool) error {
	allTrue := true
	for _, d := range defined {
		if !d {
			allTrue = false
			break
		}
	}
	if allTrue {
		return w.WriteByte(1)
	}
	if err := w.WriteByte(0); err != nil {
		return err
	}
	return writeBool(w, defined)
}

// writeSizes writes an array of sizes using 7z's variable length uint64 encoding.
func writeSizes(w io.ByteWriter, sizes []uint64) error {
	for _, s := range sizes {
		if err := writeUint64(w, s); err != nil {
			return err
		}
	}
	return nil
}

// writeCRC writes an array of CRC32 checksums, omitting those that are not defined.
func writeCRC(w byteWriter, crcs []uint32, defined []bool) error {
	if err := writeOptionalBool(w, defined); err != nil {
		return err
	}
	var buf [4]byte
	for i, c := range crcs {
		if defined[i] {
			binary.LittleEndian.PutUint32(buf[:], c)
			if _, err := w.Write(buf[:]); err != nil {
				return err
			}
		}
	}
	return nil
}