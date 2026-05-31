package rsyncreceiver

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/picosh/pico/pkg/rsync-receiver/rsync"
	"github.com/picosh/pico/pkg/rsync-receiver/utils"
)

// rsync/flist.c:receive_file_entry.
func (rt *Transfer) receiveFileEntry(flags uint16, last *utils.ReceiverFile) (*utils.ReceiverFile, error) {
	f := &utils.ReceiverFile{}

	var l1 int
	if flags&rsync.XMIT_SAME_NAME != 0 {
		l, err := rt.Conn.ReadByte()
		if err != nil {
			return nil, err
		}
		l1 = int(l)
	}

	var l2 int
	if flags&rsync.XMIT_LONG_NAME != 0 {
		l, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		l2 = int(l)
	} else {
		l, err := rt.Conn.ReadByte()
		if err != nil {
			return nil, err
		}
		l2 = int(l)
	}
	// linux/limits.h
	const PATH_MAX = 4096
	if l2 >= PATH_MAX-l1 {
		const lastname = ""
		return nil, fmt.Errorf("overflow: flags=0x%x l1=%d l2=%d lastname=%s",
			flags, l1, l2, lastname)
	}
	b := make([]byte, l1+l2)
	readb := b
	if l1 > 0 {
		copy(b, []byte(last.Name))
		readb = b[l1:]
	}
	if _, err := io.ReadFull(rt.Conn.Reader, readb); err != nil {
		return nil, err
	}
	// TODO: does rsync’s clean_fname() and sanitize_path() combination do
	// anything more than Go’s filepath.Clean()?
	f.Name = filepath.Clean(string(b))

	length, err := rt.Conn.ReadInt64()
	if err != nil {
		return nil, err
	}
	f.Length = length

	if flags&rsync.XMIT_SAME_TIME != 0 {
		f.ModTime = last.ModTime
	} else {
		modTime, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		f.ModTime = time.Unix(int64(modTime), 0)
	}

	if flags&rsync.XMIT_SAME_MODE != 0 {
		f.Mode = last.Mode
	} else {
		mode, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		f.Mode = mode
	}

	if rt.Opts.PreserveUid {
		if flags&rsync.XMIT_SAME_UID != 0 {
			f.Uid = last.Uid
		} else {
			uid, err := rt.Conn.ReadInt32()
			if err != nil {
				return nil, err
			}
			f.Uid = uid
		}
	}

	if rt.Opts.PreserveGid {
		if flags&rsync.XMIT_SAME_GID != 0 {
			f.Gid = last.Gid
		} else {
			gid, err := rt.Conn.ReadInt32()
			if err != nil {
				return nil, err
			}
			f.Gid = gid
		}
	}

	mode := f.Mode & rsync.S_IFMT
	isDev := mode == rsync.S_IFCHR || mode == rsync.S_IFBLK
	isSpecial := mode == rsync.S_IFIFO || mode == rsync.S_IFSOCK
	isLink := mode == rsync.S_IFLNK

	if rt.Opts.PreserveDevices && (isDev || isSpecial) {
		// TODO(protocol >= 28): rdev/major/minor handling
		if flags&rsync.XMIT_SAME_RDEV_pre28 != 0 {
			f.Rdev = last.Rdev
		} else {
			rdev, err := rt.Conn.ReadInt32()
			if err != nil {
				return nil, err
			}
			f.Rdev = rdev
		}
	}

	if rt.Opts.PreserveLinks && isLink {
		length, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		b := make([]byte, length)
		if _, err := io.ReadFull(rt.Conn.Reader, b); err != nil {
			return nil, err
		}
		f.LinkTarget = string(b)
	}

	return f, nil
}

// rsync/flist.c:recv_file_list.
func (rt *Transfer) ReceiveFileList() ([]*utils.ReceiverFile, error) {
	lastFileEntry := new(utils.ReceiverFile)
	var fileList []*utils.ReceiverFile
	for {
		b, err := rt.Conn.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == 0 {
			break
		}
		flags := uint16(b)
		// log.Printf("flags: %x", flags)
		// TODO(protocol >= 28): extended flags

		f, err := rt.receiveFileEntry(flags, lastFileEntry)
		if err != nil {
			return nil, err
		}
		lastFileEntry = f
		// TODO: include depth in output?
		rt.Logger.Debug("recv_file_list", "file", f.Name, "length", f.Length, "mode", f.Mode, "uid", f.Uid, "gid", f.Gid, "flags", flags)

		fileList = append(fileList, f)
	}

	utils.SortFileList(fileList)

	if rt.Opts.PreserveUid || rt.Opts.PreserveGid {
		// receive the uid/gid list
		users, groups, err := rt.RecvIdList()
		if err != nil {
			return nil, err
		}
		_ = users
		_ = groups
	}

	// read the i/o error flag
	ioErrors, err := rt.Conn.ReadInt32()
	if err != nil {
		return nil, err
	}
	rt.Logger.Debug("ioErrors", "errs", ioErrors)
	rt.IOErrors = ioErrors

	return fileList, nil
}
