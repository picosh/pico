package rsyncreceiver

import (
	"context"

	"github.com/picosh/pico/pkg/rsync-receiver/rsyncstats"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncwire"
	"github.com/picosh/pico/pkg/rsync-receiver/utils"
	"golang.org/x/sync/errgroup"
)

func (rt *Transfer) deleteFiles(fileList []*utils.ReceiverFile) error {
	if rt.IOErrors > 0 {
		rt.Logger.Debug("IO error encountered, skipping file deletion")
		return nil
	}

	return rt.Files.Remove(fileList)
}

// rsync/main.c:do_recv.
func (rt *Transfer) Do(c *rsyncwire.Conn, fileList []*utils.ReceiverFile, noReport bool) (*rsyncstats.TransferStats, error) {
	if rt.Opts.DeleteMode {
		if err := rt.deleteFiles(fileList); err != nil {
			return nil, err
		}
	}

	ctx := context.Background()
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return rt.GenerateFiles(fileList)
	})
	eg.Go(func() error {
		// Ensure we don’t block on the receiver when the generator returns an
		// error.
		errChan := make(chan error)
		go func() {
			errChan <- rt.RecvFiles(fileList)
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errChan:
			return err
		}
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	var stats *rsyncstats.TransferStats
	if !noReport {
		var err error
		stats, err = report(c)
		rt.Logger.Debug("report", "stats", stats)
		if err != nil {
			return nil, err
		}
	}

	// send final goodbye message
	if err := c.WriteInt32(-1); err != nil {
		return nil, err
	}

	return stats, nil
}

// rsync/main.c:report.
func report(c *rsyncwire.Conn) (*rsyncstats.TransferStats, error) {
	// read statistics:
	// total bytes read (from network connection)
	read, err := c.ReadInt64()
	if err != nil {
		return nil, err
	}
	// total bytes written (to network connection)
	written, err := c.ReadInt64()
	if err != nil {
		return nil, err
	}
	// total size of files
	size, err := c.ReadInt64()
	if err != nil {
		return nil, err
	}

	return &rsyncstats.TransferStats{
		Read:    read,
		Written: written,
		Size:    size,
	}, nil
}
