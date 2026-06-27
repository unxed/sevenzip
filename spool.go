package sevenzip

import (
    "sync"
	"bytes"
	"io"
	"os"
)

// spoolWriter buffers compressed data in memory up to a threshold,
// then seamlessly spills over to a temporary file on disk.
type spoolWriter struct {
	buf  *bytes.Buffer
	f    *os.File
	size int64
}

var spoolBufPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, 1024*1024))
	},
}

func newSpoolWriter() *spoolWriter {
	buf := spoolBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	return &spoolWriter{buf: buf}
}

func (s *spoolWriter) Write(p []byte) (n int, err error) {
	if s.f != nil {
		n, err = s.f.Write(p)
	} else {
		if s.size+int64(len(p)) > 4*1024*1024 { // 4MB threshold
			s.f, err = os.CreateTemp("", "szspool-*")
			if err != nil {
				return 0, err
			}
			s.f.Write(s.buf.Bytes())
			s.buf = nil
			n, err = s.f.Write(p)
		} else {
			n, err = s.buf.Write(p)
		}
	}
	s.size += int64(n)
	return n, err
}

func (s *spoolWriter) Reader() io.Reader {
	if s.f != nil {
		s.f.Seek(0, io.SeekStart)
		return s.f
	}
	return bytes.NewReader(s.buf.Bytes())
}

func (s *spoolWriter) Close() error {
	if s.f != nil {
		s.f.Close()
		os.Remove(s.f.Name())
		s.f = nil
	}
	if s.buf != nil {
		spoolBufPool.Put(s.buf)
		s.buf = nil
	}
	return nil
}