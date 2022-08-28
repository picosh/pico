package sftp

import (
	"fmt"
	"io"
	"sync"

	"git.sr.ht/~erock/pico/wish/send/utils"
)

type buffer struct {
	buf []byte
	m   sync.Mutex
	off int
}

func (b *buffer) WriteAt(p []byte, pos int64) (n int, err error) {
	pLen := len(p)
	expLen := pos + int64(pLen)
	b.m.Lock()
	defer b.m.Unlock()
	if int64(len(b.buf)) < expLen {
		if int64(cap(b.buf)) < expLen {
			newBuf := make([]byte, expLen)
			copy(newBuf, b.buf)
			b.buf = newBuf
		}
		b.buf = b.buf[:expLen]
	}
	copy(b.buf[pos:], p)
	return pLen, nil
}

func (b *buffer) Read(p []byte) (n int, err error) {
	b.m.Lock()
	defer b.m.Unlock()
	if len(b.buf) <= b.off {
		if len(p) == 0 {
			return 0, nil
		}
		return 0, io.EOF
	}
	n = copy(p, b.buf[b.off:])
	b.off += n
	return n, nil
}

type fakeWrite struct {
	fileEntry *utils.FileEntry
	handler   *handler
	buf       *buffer
}

func (f fakeWrite) WriteAt(p []byte, off int64) (int, error) {
	return f.buf.WriteAt(p, off)
}

func (f fakeWrite) Close() error {
	msg, err := f.handler.writeHandler.Write(f.handler.session, f.fileEntry)
	if err != nil {
		errMsg := fmt.Sprintf("%s\n", err.Error())
		_, err = f.handler.session.Stderr().Write([]byte(errMsg))
	}
	if msg != "" {
		nMsg := fmt.Sprintf("%s\n", msg)
		_, err = f.handler.session.Stderr().Write([]byte(nMsg))
	}
	return err
}
