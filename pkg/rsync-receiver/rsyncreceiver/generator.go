package rsyncreceiver

import (
	"fmt"
	"io"
	"os"

	"github.com/picosh/pico/pkg/rsync-receiver/rsync"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncchecksum"
	"github.com/picosh/pico/pkg/rsync-receiver/rsynccommon"
	"github.com/picosh/pico/pkg/rsync-receiver/utils"
)

// rsync/generator.c:generate_files().
func (rt *Transfer) GenerateFiles(fileList []*utils.ReceiverFile) error {
	phase := 0
	for idx, f := range fileList {
		// TODO: use a copy of f with .Mode |= S_IWUSR for directories, so
		// that we can create files within all directories.
		if err := rt.recvGenerator(idx, f); err != nil {
			return err
		}
	}
	phase++
	rt.Logger.Debug("generateFiles", "phase", phase)
	if err := rt.Conn.WriteInt32(-1); err != nil {
		return err
	}

	// TODO: re-do any files that failed
	phase++
	rt.Logger.Debug("generateFiles", "phase", phase)
	if err := rt.Conn.WriteInt32(-1); err != nil {
		return err
	}

	rt.Logger.Debug("generateFiles finished")
	return nil
}

// rsync/generator.c:skip_file.
func (rt *Transfer) skipFile(f *utils.ReceiverFile, st os.FileInfo) (bool, error) {
	if rt.Opts.AlwaysChecksum || rt.Opts.IgnoreTimes {
		return false, nil
	}

	sizeMatch := st.Size() == f.Length
	if rt.Opts.SizeOnly {
		return sizeMatch, nil
	}

	timeMatch := st.ModTime().Equal(f.ModTime)
	return sizeMatch && timeMatch, nil
}

// rsync/generator.c:recv_generator.
func (rt *Transfer) recvGenerator(idx int, f *utils.ReceiverFile) error {
	if rt.listOnly() {
		if _, err := fmt.Fprintf(rt.Env.Stdout, "%s %11.0f %s %s\n",
			f.FileMode().String(),
			float64(f.Length), // TODO: rsync prints decimal separators
			f.ModTime.Format("2006/01/02 15:04:05"),
			f.Name); err != nil {
			return err
		}
		return nil
	}
	rt.Logger.Debug("recv_generator", "file", f)

	if !f.FileMode().IsRegular() {
		// None of the Preserve* options is enabled, so just skip over
		// non-regular files.
		return nil
	}

	requestFullFile := func() error {
		rt.Logger.Debug("requesting", "file", f)
		if err := rt.Conn.WriteInt32(int32(idx)); err != nil {
			return err
		}
		if rt.Opts.DryRun {
			return nil
		}
		var sh rsync.SumHead
		if err := sh.WriteTo(rt.Conn); err != nil {
			return err
		}
		return nil
	}

	st, in, err := rt.Files.Read(&utils.SenderFile{WPath: f.Name})
	if err != nil {
		rt.Logger.Error("failed to open file", "st", st, "file", f, "err", err)
		return requestFullFile()
	}

	defer func() { _ = in.Close() }()

	skip, err := rt.skipFile(f, st)
	if err != nil {
		return err
	}

	if skip {
		rt.Logger.Debug("skipping", "file", f)
		return nil
	}

	if rt.Opts.DryRun {
		if err := rt.Conn.WriteInt32(int32(idx)); err != nil {
			return err
		}

		return nil
	}

	rt.Logger.Debug("sending sums", "file", f, "st", st)
	if err := rt.Conn.WriteInt32(int32(idx)); err != nil {
		return err
	}

	err = rt.generateAndSendSums(in, st.Size())
	if err != nil {
		rt.Logger.Error("failed to send sums", "file", f, "err", err)
	}

	return err
}

// rsync/generator.c:generate_and_send_sums.
func (rt *Transfer) generateAndSendSums(in utils.ReaderAtCloser, fileLen int64) error {
	sh := rsynccommon.SumSizesSqroot(fileLen)
	if err := sh.WriteTo(rt.Conn); err != nil {
		return err
	}
	buf := make([]byte, int(sh.BlockLength))
	remaining := fileLen
	for i := int32(0); i < sh.ChecksumCount; i++ {
		n1 := min(int64(sh.BlockLength), remaining)
		b := buf[:n1]
		if _, err := io.ReadFull(in, b); err != nil {
			return err
		}

		sum1 := rsyncchecksum.Checksum1(b)
		sum2 := rsyncchecksum.Checksum2(rt.Seed, b)
		if err := rt.Conn.WriteInt32(int32(sum1)); err != nil {
			return err
		}
		if _, err := rt.Conn.Writer.Write(sum2); err != nil {
			return err
		}
		remaining -= n1
	}
	return nil
}
