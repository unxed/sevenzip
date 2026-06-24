package util

import (
	"bytes"
	"io"
	"testing"
)

func TestBufioReadSeekCloser(t *testing.T) {
	data := []byte("0123456789abcdef")
	sr := io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data)))

	brsc := NewBufioReadSeekCloser(sr)

	// 1. Тест обычного чтения
	buf := make([]byte, 4)
	n, err := brsc.Read(buf)
	if err != nil || n != 4 || string(buf) != "0123" {
		t.Errorf("Read failed: %v, %d, %s", err, n, string(buf))
	}

	// 2. Тест ReadAt (не должен зависеть от текущего смещения в буфере)
	bufAt := make([]byte, 4)
	nAt, err := brsc.ReadAt(bufAt, 8)
	if err != nil || nAt != 4 || string(bufAt) != "89ab" {
		t.Errorf("ReadAt failed: %v, %d, %s", err, nAt, string(bufAt))
	}

	// 3. Тест Seek произвольного позиционирования
	pos, err := brsc.Seek(4, io.SeekStart)
	if err != nil || pos != 4 {
		t.Errorf("Seek failed: %v, %d", err, pos)
	}

	// 4. Тест чтения после Seek
	n, err = brsc.Read(buf)
	if err != nil || n != 4 || string(buf) != "4567" {
		t.Errorf("Read after Seek failed: %v, %d, %s", err, n, string(buf))
	}

	// 5. Тест Close
	if err := brsc.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}