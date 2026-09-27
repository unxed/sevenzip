package util

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// zeroByteReader is an io.ReadCloser whose Read always reports success
// having read zero bytes, without an error -- an edge case ReadByte must
// turn into io.ErrNoProgress rather than returning a zero byte silently.
type zeroByteReader struct{}

func (zeroByteReader) Read([]byte) (int, error) { return 0, nil }
func (zeroByteReader) Close() error             { return nil }

// failingReader is an io.ReadCloser whose Read always fails with a fixed
// error.
type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }
func (failingReader) Close() error               { return nil }

func TestNopCloser(t *testing.T) {
	rc := NopCloser(strings.NewReader("hello"))

	buf := make([]byte, 5)

	n, err := rc.Read(buf)
	if err != nil || n != 5 || string(buf) != "hello" {
		t.Fatalf("Read = %d, %v, %q", n, err, buf)
	}

	if err := rc.Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
}

func TestNopCloserReadByte(t *testing.T) {
	rc := NopCloser(strings.NewReader("A"))

	b, err := rc.ReadByte()
	if err != nil || b != 'A' {
		t.Fatalf("ReadByte = %v, %v", b, err)
	}
}

func TestByteReadCloserPassthrough(t *testing.T) {
	inner := NopCloser(strings.NewReader("passthrough"))

	if rc := ByteReadCloser(inner); rc != inner {
		t.Fatalf("ByteReadCloser should return the same instance when it already implements ReadCloser")
	}
}

func TestByteReadCloserWraps(t *testing.T) {
	inner := io.NopCloser(strings.NewReader("wrapped"))

	rc := ByteReadCloser(inner)

	if _, ok := rc.(*byteReadCloser); !ok {
		t.Fatalf("ByteReadCloser should wrap a plain io.ReadCloser instead of returning it unchanged")
	}

	for _, want := range []byte("wrapped") {
		got, err := rc.ReadByte()
		if err != nil {
			t.Fatalf("ReadByte returned error before EOF: %v", err)
		}

		if got != want {
			t.Fatalf("ReadByte = %q, want %q", got, want)
		}
	}

	if _, err := rc.ReadByte(); err != io.EOF { //nolint:errorlint
		t.Fatalf("ReadByte at end of stream = %v, want io.EOF", err)
	}
}

func TestByteReadCloserReadByteError(t *testing.T) {
	wantErr := errors.New("boom")

	rc := ByteReadCloser(failingReader{err: wantErr})

	if _, err := rc.ReadByte(); !errors.Is(err, wantErr) {
		t.Fatalf("ReadByte error = %v, want %v", err, wantErr)
	}
}

func TestByteReadCloserReadByteNoProgress(t *testing.T) {
	rc := ByteReadCloser(zeroByteReader{})

	if _, err := rc.ReadByte(); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("ReadByte = %v, want io.ErrNoProgress", err)
	}
}
