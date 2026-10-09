package rsync

import (
	"io"
	"time"
)

// FileInfo describes an entry in an FS.
type FileInfo struct {
	// Name is slash-separated and relative to the directory being listed.
	Name    string
	Size    int64
	ModTime time.Time
	IsDir   bool
}

// File is a file opened for reading.
type File interface {
	io.ReaderAt
	io.Closer
}

// FS is the storage the server reads from and writes to. Paths are
// slash-separated, relative to the root of the storage, and never start
// with a slash. The root itself is "".
type FS interface {
	// Stat returns the entry at name, or an error wrapping fs.ErrNotExist.
	Stat(name string) (FileInfo, error)
	// ReadDir returns the entries below dir with names relative to dir,
	// descending into subdirectories when recursive is set.
	ReadDir(dir string, recursive bool) ([]FileInfo, error)
	// Open opens a regular file for reading.
	Open(name string) (File, FileInfo, error)
	// Put stores a regular file with the data read from r. If the transfer
	// fails, r returns an error instead of io.EOF and Put must not keep
	// what it read. The returned message, if any, is shown to the user.
	Put(name string, info FileInfo, r io.Reader) (string, error)
	// Remove deletes a file or an empty directory.
	Remove(name string, isDir bool) error
}

// mapFile gives windowed access to a file being sent, like rsync's
// map_ptr. Returned slices are valid until the next call. After a read error
// it returns zeroes and records the error, which the caller checks once the
// file is done.
type mapFile struct {
	r       io.ReaderAt
	size    int64
	window  []byte
	start   int64
	defSize int64
	err     error
}

func newMapFile(r io.ReaderAt, size int64, readSize int64, blength int32) *mapFile {
	if blength > 0 && readSize%int64(blength) != 0 {
		readSize += int64(blength) - readSize%int64(blength)
	}
	return &mapFile{r: r, size: size, defSize: alignUp(readSize)}
}

func alignUp(n int64) int64 { return (n + 1023) &^ 1023 }

func (m *mapFile) ptr(offset int64, n int) []byte {
	if n == 0 {
		return nil
	}
	if m.err != nil {
		return make([]byte, n)
	}
	end := offset + int64(n)
	if offset >= m.start && end <= m.start+int64(len(m.window)) {
		return m.window[offset-m.start : end-m.start]
	}
	start := offset &^ 1023
	size := max(m.defSize, alignUp(end-start))
	if start+size > m.size {
		size = m.size - start
	}
	buf := make([]byte, size)
	// Reuse the overlap with the current window.
	read := buf
	readAt := start
	if start >= m.start && start < m.start+int64(len(m.window)) {
		k := copy(buf, m.window[start-m.start:])
		read = buf[k:]
		readAt = start + int64(k)
	}
	if len(read) > 0 {
		if err := readFullAt(m.r, read, readAt); err != nil {
			m.err = err
			return make([]byte, n)
		}
	}
	m.window = buf
	m.start = start
	return m.window[offset-m.start : end-m.start]
}
