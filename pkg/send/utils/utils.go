package utils

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/picosh/pico/pkg/pssh"
)

// NULL is an array with a single NULL byte.
var NULL = []byte{'\x00'}

// ErrFileIgnored marks a file deliberately excluded by upload policy.
var ErrFileIgnored = errors.New("file ignored")

// FileEntry is an Entry that reads from a Reader, defining a file and
// its contents.
type FileEntry struct {
	Filepath string
	Mode     fs.FileMode
	Size     int64
	Reader   io.Reader
	Atime    int64
	Mtime    int64
	Metadata map[string]string
}

// Write a file to the given writer.
func (e *FileEntry) Write(w io.Writer) error {
	if e.Mtime > 0 && e.Atime > 0 {
		if _, err := fmt.Fprintf(w, "T%d 0 %d 0\n", e.Mtime, e.Atime); err != nil {
			return fmt.Errorf("failed to write file: %q: %w", e.Filepath, err)
		}
	}
	fname := filepath.Base(e.Filepath)
	if _, err := fmt.Fprintf(w, "C%s %d %s\n", octalPerms(e.Mode), e.Size, fname); err != nil {
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
	Delete(*pssh.SSHServerConnSession, *FileEntry) error
	Write(*pssh.SSHServerConnSession, *FileEntry) (string, error)
	Read(*pssh.SSHServerConnSession, *FileEntry) (os.FileInfo, ReadAndReaderAtCloser, error)
	List(*pssh.SSHServerConnSession, string, bool, bool) ([]os.FileInfo, error)
	GetLogger(*pssh.SSHServerConnSession) *slog.Logger
	Validate(*pssh.SSHServerConnSession) error
}

// WriteValidator is implemented by handlers that can check a write before
// its contents arrive, so SFTP can refuse a file when it is opened.
type WriteValidator interface {
	ValidateWrite(*pssh.SSHServerConnSession, *FileEntry) error
}

// FileRenamer is implemented by handlers that can move a stored file in
// place, replacing any file at the destination. It returns
// errors.ErrUnsupported for a move it can't make, and SFTP copies the file
// instead.
type FileRenamer interface {
	Rename(s *pssh.SSHServerConnSession, from, to *FileEntry) error
}

func KeyText(session *pssh.SSHServerConnSession) (string, error) {
	if session.PublicKey() == nil {
		return "", fmt.Errorf("session doesn't have public key")
	}
	kb := base64.StdEncoding.EncodeToString(session.PublicKey().Marshal())
	return fmt.Sprintf("%s %s", session.PublicKey().Type(), kb), nil
}

func ErrorHandler(session *pssh.SSHServerConnSession, err error) {
	_, _ = fmt.Fprint(session.Stderr(), err, "\r\n")
	_ = session.Exit(1)
	_ = session.Close()
}

func PrintMsg(session *pssh.SSHServerConnSession, stdout []string, stderr []error) {
	output := ""
	if len(stdout) > 0 {
		for _, msg := range stdout {
			if msg != "" {
				output += fmt.Sprintf("%s\r\n", msg)
			}
		}
		_, _ = fmt.Fprintln(session.Stderr(), output)
	}

	outputErr := ""
	if len(stderr) > 0 {
		for _, err := range stderr {
			outputErr += fmt.Sprintf("%v\r\n", err)
		}
		_, _ = fmt.Fprintln(session.Stderr(), outputErr)
	}
}
