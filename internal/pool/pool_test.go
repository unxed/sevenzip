package pool

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReader struct {
	closed   int
	closeErr error
}

func (*fakeReader) Read([]byte) (int, error)       { return 0, io.EOF }
func (*fakeReader) Seek(int64, int) (int64, error) { return 0, nil }
func (*fakeReader) Size() int64                    { return 0 }
func (f *fakeReader) Close() error                 { f.closed++; return f.closeErr }

func TestPoolCloseClosesEveryPooledReader(t *testing.T) {
	t.Parallel()

	p, err := NewPool()
	require.NoError(t, err)

	readers := []*fakeReader{{}, {}, {}}
	for i, r := range readers {
		_, err := p.Put(int64(i+1)*10, r)
		require.NoError(t, err)
	}

	require.NoError(t, p.Close())

	for i, r := range readers {
		assert.Equal(t, 1, r.closed, "reader %d must be closed exactly once", i)
	}

	// The pool stays usable and is empty after Close; a second Close is a no-op.
	_, ok := p.Get(100)
	assert.False(t, ok)
	require.NoError(t, p.Close())

	for i, r := range readers {
		assert.Equal(t, 1, r.closed, "reader %d must not be closed twice", i)
	}
}

func TestPoolCloseReportsErrorsButClosesAll(t *testing.T) {
	t.Parallel()

	p, err := NewPool()
	require.NoError(t, err)

	errBoom := errors.New("boom")
	bad, good := &fakeReader{closeErr: errBoom}, &fakeReader{}

	_, err = p.Put(10, bad)
	require.NoError(t, err)
	_, err = p.Put(20, good)
	require.NoError(t, err)

	require.ErrorIs(t, p.Close(), errBoom)
	assert.Equal(t, 1, bad.closed)
	assert.Equal(t, 1, good.closed, "a failing Close must not leave the others open")
}

func TestNoopPoolClose(t *testing.T) {
	t.Parallel()

	p, err := NewNoopPool()
	require.NoError(t, err)
	require.NoError(t, p.Close())
}
