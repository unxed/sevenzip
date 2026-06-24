// Package util implements various utility types and interfaces.
package util

import (
	"bufio"
	"io"
)

// SizeReadSeekCloser is an io.Reader, io.Seeker, and io.Closer with a Size
// method.
type SizeReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
	Size() int64
}

// Reader is both an io.Reader and io.ByteReader.
type Reader interface {
	io.Reader
	io.ByteReader
}

// ReadCloser is a Reader that is also an io.Closer.
type ReadCloser interface {
	Reader
	io.Closer
}

type nopCloser struct {
	Reader
}

func (nopCloser) Close() error {
	return nil
}

// NopCloser returns a ReadCloser with a no-op Close method wrapping the
// provided Reader r.
func NopCloser(r Reader) ReadCloser {
	return &nopCloser{r}
}

type byteReadCloser struct {
	io.ReadCloser
}

func (rc *byteReadCloser) ReadByte() (byte, error) {
	var b [1]byte

	n, err := rc.Read(b[:])
	if err != nil {
		return 0, err //nolint:wrapcheck
	}

	if n == 0 {
		return 0, io.ErrNoProgress
	}

	return b[0], nil
}

// ByteReadCloser returns a ReadCloser either by returning the io.ReadCloser
// r if it implements the interface, or wrapping it with a ReadByte method.
func ByteReadCloser(r io.ReadCloser) ReadCloser {
	if rc, ok := r.(ReadCloser); ok {
		return rc
	}

	return &byteReadCloser{r}
}
// BufioReadSeekCloser wraps a SectionReader to provide buffered sequential
// reading while preserving the ReaderAt and Seeker interfaces for parallel processing.
type BufioReadSeekCloser struct {
	*bufio.Reader
	sr *io.SectionReader
}

func NewBufioReadSeekCloser(sr *io.SectionReader) *BufioReadSeekCloser {
	return &BufioReadSeekCloser{
		Reader: bufio.NewReader(sr),
		sr:     sr,
	}
}

func (b *BufioReadSeekCloser) ReadAt(p []byte, off int64) (int, error) {
	return b.sr.ReadAt(p, off)
}

func (b *BufioReadSeekCloser) Seek(offset int64, whence int) (int64, error) {
	n, err := b.sr.Seek(offset, whence)
	b.Reader.Reset(b.sr)
	return n, err
}

func (b *BufioReadSeekCloser) Close() error {
	return nil
}
