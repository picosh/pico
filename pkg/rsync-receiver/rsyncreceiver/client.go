package rsyncreceiver

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/picosh/pico/pkg/rsync-receiver/rsync"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncopts"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncsender"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncwire"
	"github.com/picosh/pico/pkg/rsync-receiver/utils"
)

func ClientRun(logger *slog.Logger, opts *rsyncopts.Options, conn io.ReadWriter, filesystem utils.FS, paths []string, negotiate bool) error {
	var err error

	crd, cwr := rsyncwire.CounterPair(conn, conn)

	const sessionChecksumSeed = 666

	c := &rsyncwire.Conn{
		Reader: crd,
		Writer: cwr,
	}

	if negotiate {
		remoteProtocol, err := c.ReadInt32()
		if err != nil {
			return err
		}
		logger.Debug("remote protocol", "protocol", remoteProtocol)
		if err := c.WriteInt32(rsync.ProtocolVersion); err != nil {
			return err
		}
	}

	if err := c.WriteInt32(sessionChecksumSeed); err != nil {
		return err
	}

	// Switch to multiplexing protocol, but only for server-side transmissions.
	// Transmissions received from the client are not multiplexed.
	mpx := &rsyncwire.MultiplexWriter{Writer: c.Writer}
	c.Writer = mpx

	defer func() {
		if err != nil {
			_, _ = mpx.WriteMsg(rsyncwire.MsgError, fmt.Appendf(nil, "gokr-rsync [receiver]: %v\n", err))
		}
	}()

	rt := &Transfer{
		Opts: &TransferOpts{
			DryRun: opts.DryRun(),

			DeleteMode:       opts.DeleteMode(),
			PreserveGid:      opts.PreserveGid(),
			PreserveUid:      opts.PreserveUid(),
			PreserveLinks:    opts.PreserveLinks(),
			PreservePerms:    opts.PreservePerms(),
			PreserveDevices:  opts.PreserveDevices(),
			PreserveSpecials: opts.PreserveSpecials(),
			PreserveTimes:    opts.PreserveMTimes(),
			IgnoreTimes:      opts.IgnoreTimes(),
			SizeOnly:         opts.SizeOnly(),
			AlwaysChecksum:   opts.AlwaysChecksum(),
			// TODO: PreserveHardlinks: opts.PreserveHardlinks,
		},
		Dest: "/",
		// TODO: what is Env used for and can we get rid of it?
		Env: Osenv{
			Stdout: os.Stdout,
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
		},
		Conn: c,
		Seed: sessionChecksumSeed,

		Files: filesystem,

		Logger: logger,
	}

	if opts.DeleteMode() {
		// receive the exclusion list (openrsync’s is always empty)
		exclusionList, err := rsyncsender.RecvFilterList(c)
		if err != nil {
			return err
		}
		logger.Debug("exclusion list read", "filters", exclusionList.Filters)
	}

	// receive file list
	logger.Debug("receiving file list")
	fileList, err := rt.ReceiveFileList()
	if err != nil {
		return err
	}
	logger.Debug("received names", "files", fileList)
	stats, err := rt.Do(c, fileList, true)
	if err != nil {
		return err
	}

	logger.Debug("stats", "stats", stats)
	return nil
}
