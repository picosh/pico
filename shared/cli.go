package shared

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

type CmdSessionLogger struct {
	Log *slog.Logger
}

func (c *CmdSessionLogger) Write(out []byte) (int, error) {
	c.Log.Info(string(out))
	return 0, nil
}

func (c *CmdSessionLogger) Exit(code int) error {
	os.Exit(code)
	return fmt.Errorf("panic %d", code)
}

func (c *CmdSessionLogger) Close() error {
	return fmt.Errorf("closing")
}

func (c *CmdSessionLogger) Stderr() io.ReadWriter {
	return nil
}

type CmdSession interface {
	Write([]byte) (int, error)
	Exit(code int) error
	Close() error
	Stderr() io.ReadWriter
}
