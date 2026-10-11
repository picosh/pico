package sftp

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/picosh/pico/pkg/send/utils"
)

type buffer struct {
	buf []byte
	m   sync.Mutex
}

func (b *buffer) WriteAt(p []byte, pos int64) (n int, err error) {
	pLen := len(p)
	expLen := pos + int64(pLen)
	b.m.Lock()
	defer b.m.Unlock()
	if int64(len(b.buf)) < expLen {
		oldLen := len(b.buf)
		if int64(cap(b.buf)) < expLen {
			newBuf := make([]byte, expLen)
			copy(newBuf, b.buf)
			b.buf = newBuf
		}
		// A truncated buffer keeps its old bytes past its length.
		b.buf = b.buf[:expLen]
		clear(b.buf[oldLen:])
	}
	copy(b.buf[pos:], p)
	return pLen, nil
}

func (b *buffer) truncate(size int64) {
	b.m.Lock()
	defer b.m.Unlock()
	if size < int64(len(b.buf)) {
		b.buf = b.buf[:size]
		return
	}
	b.buf = append(b.buf, make([]byte, size-int64(len(b.buf)))...)
}

func (b *buffer) bytes() []byte {
	b.m.Lock()
	defer b.m.Unlock()
	return bytes.Clone(b.buf)
}

// pendingFile is a file open for writing. Editors over sshfs stat a file
// right after creating it, so it is listed from here until the last handle
// closes and its contents are written.
type pendingFile struct {
	path    string
	entry   *utils.FileEntry
	buf     *buffer
	modTime time.Time
	handles int
	// stored is set when storage had the file at open. Unless a handle
	// truncated or wrote to it, it's left as it is.
	stored bool
	dirty  bool
	// replaces holds stored files this one was renamed from while open.
	// They are deleted once it is stored at its new path.
	replaces []renamedFile
}

// renamedFile is a stored file renamed away, with the checksum it had then.
// Another session may replace it before it's deleted.
type renamedFile struct {
	path string
	sum  []byte
}

func (p *pendingFile) info(name string) *utils.VirtualFile {
	return &utils.VirtualFile{
		FName:    name,
		FSize:    int64(len(p.buf.bytes())),
		FModTime: p.modTime,
	}
}

type pendingWriter struct {
	handler *handler
	file    *pendingFile
}

func (w *pendingWriter) WriteAt(p []byte, off int64) (int, error) {
	w.handler.mu.Lock()
	w.file.dirty = true
	w.handler.mu.Unlock()
	return w.file.buf.WriteAt(p, off)
}

func (w *pendingWriter) Close() error {
	h, f := w.handler, w.file
	h.mu.Lock()
	f.handles--
	// A file removed while open is dropped.
	commit := f.handles == 0 && h.pending[f.path] == f
	if commit {
		delete(h.pending, f.path)
	}
	name := f.path
	h.mu.Unlock()
	if !commit || (f.stored && !f.dirty) {
		return nil
	}

	data := f.buf.bytes()
	entry := *f.entry
	entry.Filepath = name
	entry.Reader = bytes.NewReader(data)
	entry.Size = int64(len(data))
	msg, err := h.writeHandler.Write(h.session, &entry)
	h.mu.Lock()
	replaced := h.release(f)
	h.mu.Unlock()
	if err != nil {
		_, _ = fmt.Fprintf(h.session.Stderr(), "%s\r\n", err)
		return err
	}
	if msg != "" {
		_, _ = fmt.Fprintf(h.session.Stderr(), "%s\r\n", msg)
	}
	for _, old := range replaced {
		if err := h.deleteIfUnchanged(old.path, old.sum); err != nil {
			_, _ = fmt.Fprintf(h.session.Stderr(), "%s\r\n", err)
			return err
		}
	}
	return nil
}

// ignoredWriter accepts a file the handler ignores by policy, so a
// transfer moves on to the next one.
type ignoredWriter struct {
	handler *handler
	path    string
}

func (ignoredWriter) WriteAt(p []byte, _ int64) (int, error) { return len(p), nil }

func (w ignoredWriter) Close() error {
	w.handler.mu.Lock()
	defer w.handler.mu.Unlock()
	w.handler.ignored[w.path]--
	if w.handler.ignored[w.path] <= 0 {
		delete(w.handler.ignored, w.path)
	}
	return nil
}
