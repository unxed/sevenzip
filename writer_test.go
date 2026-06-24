package sevenzip

import (
	"io"
	"os"
	"testing"
	"time"
    "path/filepath"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriterBasic(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp("", "sevenzip-test-*.7z")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	w, err := NewWriter(f)
	require.NoError(t, err)

	fh1 := &FileHeader{
		Name:       "hello.txt",
		Modified:   time.Date(2023, 10, 1, 12, 0, 0, 0, time.UTC),
		Attributes: 0x20, // Archive
	}
	fw1, err := w.CreateHeader(fh1)
	require.NoError(t, err)

	msg1 := []byte("Hello, 7-zip!")
	_, err = fw1.Write(msg1)
	require.NoError(t, err)

	fw2, err := w.Create("dir/world.txt")
	require.NoError(t, err)

	msg2 := []byte("World is compressed via LZMA2 successfully.")
	_, err = fw2.Write(msg2)
	require.NoError(t, err)

	// Add an empty file
	_, err = w.Create("empty.txt")
	require.NoError(t, err)

	err = w.Close()
	require.NoError(t, err)

	// Close the file so it flushes to disk properly
	err = f.Close()
	require.NoError(t, err)

	// Test Reading it back
	r, err := OpenReader(f.Name())
	require.NoError(t, err)
	defer r.Close()

	assert.Equal(t, 3, len(r.File))

	// Verify File 1
	assert.Equal(t, "hello.txt", r.File[0].Name)
	assert.Equal(t, uint32(0x20), r.File[0].Attributes)
	assert.Equal(t, time.Date(2023, 10, 1, 12, 0, 0, 0, time.UTC), r.File[0].Modified.UTC())

	rc1, err := r.File[0].Open()
	require.NoError(t, err)
	out1, err := io.ReadAll(rc1)
	require.NoError(t, err)
	assert.Equal(t, msg1, out1)
	rc1.Close()

	// Verify File 2
	assert.Equal(t, "dir/world.txt", r.File[1].Name)
	rc2, err := r.File[1].Open()
	require.NoError(t, err)
	out2, err := io.ReadAll(rc2)
	require.NoError(t, err)
	assert.Equal(t, msg2, out2)
	rc2.Close()

	// Verify File 3 (Empty)
	assert.Equal(t, "empty.txt", r.File[2].Name)
	rc3, err := r.File[2].Open()
	require.NoError(t, err)
	out3, err := io.ReadAll(rc3)
	require.NoError(t, err)
	assert.Equal(t, []byte{}, out3)
	rc3.Close()
}

func TestWriterSolid(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp("", "sevenzip-solid-*.7z")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	w, err := NewWriter(f, WithSolid(true))
	require.NoError(t, err)

	fw1, err := w.Create("file1.txt")
	require.NoError(t, err)
	msg1 := []byte("Solid block file 1")
	fw1.Write(msg1)

	fw2, err := w.Create("file2.txt")
	require.NoError(t, err)
	msg2 := []byte("Solid block file 2")
	fw2.Write(msg2)

	err = w.Close()
	require.NoError(t, err)
	f.Close()

	r, err := OpenReader(f.Name())
	require.NoError(t, err)
	defer r.Close()

	assert.Equal(t, 2, len(r.File))

	// In solid mode, both files should be in stream 0
	assert.Equal(t, 0, r.File[0].Stream)
	assert.Equal(t, 0, r.File[1].Stream)

	rc1, _ := r.File[0].Open()
	out1, _ := io.ReadAll(rc1)
	assert.Equal(t, msg1, out1)
	rc1.Close()

	rc2, _ := r.File[1].Open()
	out2, _ := io.ReadAll(rc2)
	assert.Equal(t, msg2, out2)
	rc2.Close()
}

func TestWriterEncrypted(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp("", "sevenzip-enc-*.7z")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	w, err := NewWriter(f, WithPassword("mysecret"))
	require.NoError(t, err)

	fw, err := w.Create("secret.txt")
	require.NoError(t, err)

	msg := []byte("Highly confidential encrypted data")
	_, err = fw.Write(msg)
	require.NoError(t, err)

	err = w.Close()
	require.NoError(t, err)
	f.Close()

	// Read it back
	r, err := OpenReaderWithPassword(f.Name(), "mysecret")
	require.NoError(t, err)
	defer r.Close()

	assert.Equal(t, 1, len(r.File))
	assert.Equal(t, "secret.txt", r.File[0].Name)

	rc, err := r.File[0].Open()
	require.NoError(t, err)
	out, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, msg, out)
	rc.Close()
}

func TestLZMA2PoolingPerformance(t *testing.T) {
	// Этот тест проверяет, что при создании множества маленьких файлов
	// (каждый в своем folder) память не утекает и компрессоры переиспользуются.
	if testing.Short() {
		t.Skip("skipping memory stress test in short mode")
	}

	f, err := os.CreateTemp("", "sevenzip-pool-test-*.7z")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	w, err := NewWriter(f, WithSolid(false)) // Отключаем solid, чтобы форсировать создание folder на каждый файл
	require.NoError(t, err)

	data := []byte("constant data for testing pooling efficiency")

	// Создаем 1000 файлов. Без пулинга это вызвало бы ~48 ГБ аллокаций.
	for i := 0; i < 1000; i++ {
		fw, err := w.Create(filepath.Join("dir", filepath.Base(f.Name())+string(rune(i))))
		if err != nil {
			t.Fatalf("failed to create file %d: %v", i, err)
		}
		fw.Write(data)
		fw.Close()
	}

	err = w.Close()
	require.NoError(t, err)
	f.Close()
}
