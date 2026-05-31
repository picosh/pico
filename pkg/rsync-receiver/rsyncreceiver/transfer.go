package rsyncreceiver

import (
	"io"
	"log/slog"

	"github.com/picosh/pico/pkg/rsync-receiver/rsyncwire"
	"github.com/picosh/pico/pkg/rsync-receiver/utils"
)

type Osenv struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// TransferOpts is a subset of Opts which is required for implementing a receiver.
type TransferOpts struct {
	Verbose bool
	DryRun  bool

	DeleteMode        bool
	PreserveGid       bool
	PreserveUid       bool
	PreserveLinks     bool
	PreservePerms     bool
	PreserveDevices   bool
	PreserveSpecials  bool
	PreserveTimes     bool
	PreserveHardlinks bool
	IgnoreTimes       bool
	SizeOnly          bool
	AlwaysChecksum    bool
}

type Transfer struct {
	// config
	// Opts *Opts
	Opts *TransferOpts
	Dest string
	Env  Osenv

	// state
	Conn     *rsyncwire.Conn
	Seed     int32
	IOErrors int32

	Files utils.FS

	Logger *slog.Logger
}

func (rt *Transfer) listOnly() bool { return rt.Dest == "" }
