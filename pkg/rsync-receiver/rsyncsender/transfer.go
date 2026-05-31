package rsyncsender

import (
	"io"
	"log/slog"

	"github.com/picosh/pico/pkg/rsync-receiver/rsyncopts"
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
}

type Transfer struct {
	// config
	// Opts *Opts
	Opts *rsyncopts.Options

	// state
	Conn      *rsyncwire.Conn
	Seed      int32
	lastMatch int64

	Files utils.FS

	Logger *slog.Logger
}

//func (rt *Transfer) listOnly() bool { return rt.Dest == "" }
