package sftp

import (
	"bytes"
	"fmt"

	"git.sr.ht/~erock/pico/wish/send/utils"
)

type fakeWrite struct {
	fileEntry *utils.FileEntry
	handler   *handler
	buf       *bytes.Buffer
}

func (f fakeWrite) WriteAt(p []byte, off int64) (int, error) {
	return f.buf.Write(p)
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
