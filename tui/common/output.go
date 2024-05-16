package common

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/kr/pty"
	"github.com/muesli/termenv"
)

// Bridge Wish and Termenv so we can query for a user's terminal capabilities.
type sshOutput struct {
	ssh.Session
	tty *os.File
}

func (s *sshOutput) Write(p []byte) (int, error) {
	return s.Session.Write(p)
}

func (s *sshOutput) Read(p []byte) (int, error) {
	return s.Session.Read(p)
}

func (s *sshOutput) Fd() uintptr {
	return s.tty.Fd()
}

type sshEnviron struct {
	environ []string
}

func (s *sshEnviron) Getenv(key string) string {
	for _, v := range s.environ {
		if strings.HasPrefix(v, key+"=") {
			return v[len(key)+1:]
		}
	}
	return ""
}

func (s *sshEnviron) Environ() []string {
	return s.environ
}

// Create a termenv.Output from the session.
func OutputFromSession(sesh ssh.Session) *termenv.Output {
	sshPty, _, _ := sesh.Pty()
	_, tty, err := pty.Open()
	if err != nil {
		wish.Fatalln(sesh, err)
		return nil
	}
	o := &sshOutput{
		Session: sesh,
		tty:     tty,
	}
	environ := sesh.Environ()
	environ = append(environ, fmt.Sprintf("TERM=%s", sshPty.Term))
	e := &sshEnviron{environ: environ}
	// We need to use unsafe mode here because the ssh session is not running
	// locally and we already know that the session is a TTY.
	return termenv.NewOutput(o, termenv.WithUnsafe(), termenv.WithEnvironment(e))
}
