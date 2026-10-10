package scp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/send/utils"
)

var (
	reTimestamp = regexp.MustCompile(`^T(\d{10}) 0 (\d{10}) 0$`)
	reNewFolder = regexp.MustCompile(`^D(\d{4}) 0 (.*)$`)
	reNewFile   = regexp.MustCompile(`^C(\d{4}) (\d+) (.*)$`)
)

type parseError struct {
	subject string
}

func (e parseError) Error() string {
	return fmt.Sprintf("failed to parse: %q", e.subject)
}

func copyFromClient(session *pssh.SSHServerConnSession, info Info, handler utils.CopyFromClientHandler) error {
	// accepts the request
	_, _ = session.Write(utils.NULL)

	writeErrors := []error{}
	writeSuccess := []string{}

	var (
		path  = info.Path
		r     = bufio.NewReader(session)
		mtime int64
		atime int64
	)

	for {
		line, _, err := r.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("failed to read line: %w", err)
		}

		if matches := reTimestamp.FindAllStringSubmatch(string(line), 2); matches != nil {
			mtime, err = strconv.ParseInt(matches[0][1], 10, 64)
			if err != nil {
				return parseError{string(line)}
			}
			atime, err = strconv.ParseInt(matches[0][2], 10, 64)
			if err != nil {
				return parseError{string(line)}
			}

			// accepts the header
			_, _ = session.Write(utils.NULL)
			continue
		}

		if matches := reNewFile.FindAllStringSubmatch(string(line), 3); matches != nil {
			if len(matches) != 1 || len(matches[0]) != 4 {
				return parseError{string(line)}
			}

			mode, err := strconv.ParseUint(matches[0][1], 8, 32)
			if err != nil {
				return parseError{string(line)}
			}

			size, err := strconv.ParseInt(matches[0][2], 10, 64)
			if err != nil {
				return parseError{string(line)}
			}
			name := matches[0][3]

			// accepts the header
			_, _ = session.Write(utils.NULL)

			reader := utils.NewLimitReader(r, int(size))
			result, err := handler.Write(session, &utils.FileEntry{
				Filepath: filepath.Join(path, name),
				Mode:     fs.FileMode(mode),
				Size:     size,
				Mtime:    mtime,
				Atime:    atime,
				Reader:   reader,
			})
			// A handler that rejects a file may not read all of it. Skip
			// the rest so the next header is read from the right place.
			if _, derr := io.Copy(io.Discard, reader); derr != nil {
				return fmt.Errorf("failed to read file: %q: %w", name, derr)
			}

			switch {
			case err == nil:
				writeSuccess = append(writeSuccess, result)
			case errors.Is(err, utils.ErrFileIgnored):
				writeErrors = append(writeErrors, err)
			default:
				fmt.Printf("failed to write file: %q: %v\n", name, err)
			}

			// read the trailing nil char
			_, _ = r.ReadByte() // TODO: check if it is indeed a utils.NULL?

			mtime = 0
			atime = 0
			if err != nil && !errors.Is(err, utils.ErrFileIgnored) {
				// A warning reply makes the client report the file and
				// exit non-zero once the rest are sent.
				msg := strings.ReplaceAll(err.Error(), "\n", " ")
				_, _ = fmt.Fprintf(session, "\x01%s\n", msg)
				continue
			}
			// says 'hey im done'
			_, _ = session.Write(utils.NULL)
			continue
		}

		if matches := reNewFolder.FindAllStringSubmatch(string(line), 2); matches != nil {
			if len(matches) != 1 || len(matches[0]) != 3 {
				return parseError{string(line)}
			}

			name := matches[0][2]
			path = filepath.Join(path, name)
			// says 'hey im done'
			_, _ = session.Write(utils.NULL)
			continue
		}

		if string(line) == "E" {
			path = filepath.Dir(path)

			// says 'hey im done'
			_, _ = session.Write(utils.NULL)
			continue
		}

		return fmt.Errorf("unhandled input: %q", string(line))
	}

	utils.PrintMsg(session, writeSuccess, writeErrors)

	_, _ = session.Write(utils.NULL)
	return nil
}
