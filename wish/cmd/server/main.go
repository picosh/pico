package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"git.sr.ht/~erock/pico/wish/send"
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
)

type handler struct {
}

func (h *handler) Write(session ssh.Session, file *utils.FileEntry) (string, error) {
	str := fmt.Sprintf("Received file: %+v from session: %+v", file, session)
	log.Print(str)
	return str, nil
}

func (h *handler) Validate(session ssh.Session) error {
	log.Printf("Received validate from session: %+v", session)

	return nil
}

func (h *handler) Read(session ssh.Session, filename string) (os.FileInfo, io.ReaderAt, error) {
	log.Printf("Received validate from session: %+v", session)

	data := strings.NewReader("lorem ipsum dolor")

	return &utils.VirtualFile{
		FName:    "test",
		FIsDir:   false,
		FSize:    data.Size(),
		FModTime: time.Now(),
	}, data, nil
}

func (h *handler) List(session ssh.Session, filename string) ([]os.FileInfo, error) {
	return nil, nil
}

func main() {
	h := &handler{}

	s, err := wish.NewServer(
		wish.WithAddress("localhost:9000"),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		send.Middleware(h),
	)

	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting ssh server on 9000")

	log.Fatal(s.ListenAndServe())
}
