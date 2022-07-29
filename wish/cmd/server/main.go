package main

import (
	"fmt"
	"log"

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

func main() {
	h := &handler{}

	s, err := wish.NewServer(
		wish.WithAddress(":9000"),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		send.Middleware(h),
	)

	if err != nil {
		log.Fatal(err)
	}

	log.Println("Starting ssh server on 9000")

	log.Fatal(s.ListenAndServe())
}
