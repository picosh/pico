package shared

import (
	"io"
)

type CmdSession interface {
	Write([]byte) (int, error)
	Exit(code int) error
	Close() error
	Stderr() io.ReadWriter
}
