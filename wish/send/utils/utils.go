package utils

import (
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"

	"github.com/gliderlabs/ssh"
)

// NULL is an array with a single NULL byte.
var NULL = []byte{'\x00'}

// FileEntry is an Entry that reads from a Reader, defining a file and
// its contents.
type FileEntry struct {
	Name     string
	Filepath string
	Mode     fs.FileMode
	Size     int64
	Reader   io.Reader
	Atime    int64
	Mtime    int64
}

// Write a file to the given writer.
func (e *FileEntry) Write(w io.Writer) error {
	if e.Mtime > 0 && e.Atime > 0 {
		if _, err := fmt.Fprintf(w, "T%d 0 %d 0\n", e.Mtime, e.Atime); err != nil {
			return fmt.Errorf("failed to write file: %q: %w", e.Filepath, err)
		}
	}
	if _, err := fmt.Fprintf(w, "C%s %d %s\n", octalPerms(e.Mode), e.Size, e.Name); err != nil {
		return fmt.Errorf("failed to write file: %q: %w", e.Filepath, err)
	}

	if _, err := io.Copy(w, e.Reader); err != nil {
		return fmt.Errorf("failed to read file: %q: %w", e.Filepath, err)
	}

	if _, err := w.Write(NULL); err != nil {
		return fmt.Errorf("failed to write file: %q: %w", e.Filepath, err)
	}
	return nil
}

func octalPerms(info fs.FileMode) string {
	return "0" + strconv.FormatUint(uint64(info.Perm()), 8)
}

// CopyFromClientHandler is a handler that can be implemented to handle files
// being copied from the client to the server.
type CopyFromClientHandler interface {
	// Write should write the given file.
	Write(ssh.Session, *FileEntry) (string, error)
	Read(ssh.Session, string) (os.FileInfo, io.ReaderAt, error)
	List(ssh.Session, string) ([]os.FileInfo, error)
	Validate(ssh.Session) error
}

func KeyText(session ssh.Session) (string, error) {
	if session.PublicKey() == nil {
		return "", fmt.Errorf("session doesn't have public key")
	}
	kb := base64.StdEncoding.EncodeToString(session.PublicKey().Marshal())
	return fmt.Sprintf("%s %s", session.PublicKey().Type(), kb), nil
}

func ErrorHandler(session ssh.Session, err error) {
	_, _ = fmt.Fprintln(session.Stderr(), err)
	_ = session.Exit(1)
	_ = session.Close()
}

func PrintMsg(session ssh.Session, stdout []string, stderr []error) {
	output := ""
	if len(stdout) > 0 {
		for _, msg := range stdout {
			if msg != "" {
				output += fmt.Sprintf("%s\n", msg)
			}
		}
		_, _ = fmt.Fprintln(session.Stderr(), output)
	}

	outputErr := ""
	if len(stderr) > 0 {
		for _, err := range stderr {
			outputErr += fmt.Sprintf("%v\n", err)
		}
		_, _ = fmt.Fprintln(session.Stderr(), outputErr)
	}
}
