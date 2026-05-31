package rsyncsender

import (
	"os"
	"os/user"
	"strconv"
	"sync"

	"github.com/picosh/pico/pkg/rsync-receiver/rsync"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncchecksum"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncopts"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncwire"
	"github.com/picosh/pico/pkg/rsync-receiver/utils"
)

type fileList struct {
	TotalSize int64
	Files     []utils.SenderFile
}

// rsync/rsync.h defines chunkSize as 32 * 1024, but increasing it to 256K
// increases throughput with “tridge” rsync as client by 50 Mbit/s.
const chunkSize = 256 * 1024

var (
	lookupOnce      sync.Once
	lookupGroupOnce sync.Once
)

// rsync/flist.c:send_file_list.
func (st *Transfer) SendFileList(opts *rsyncopts.Options, paths []string, excl *filterRuleList) (*fileList, error) {
	var fileList fileList
	fec := &rsyncwire.Buffer{}

	uidMap := make(map[int32]string)
	gidMap := make(map[int32]string)

	// TODO: flush in between to keep the pipes filled when traversal takes long

	// TODO: handle info == nil case (permission denied?): should set an i/o
	// error flag, but traversal should continue

	st.Logger.Debug("sendFileList()")
	// TODO: handle |root| referring to an individual file, symlink or special (skip)
	for _, requested := range paths {
		files, err := st.Files.List(requested)
		if err != nil {
			return nil, err
		}

		for _, info := range files {
			// Only ever transmit long names, like openrsync
			flags := byte(rsync.XMIT_LONG_NAME)

			// log.Printf("Trim(path=%q, %q) = %q", path, strip, name)
			name := info.Name()
			path := name
			if name == "/" {
				name = "."
				flags |= rsync.XMIT_TOP_DIR
			}
			// log.Printf("flags for %q: %v", name, flags)

			if excl.matches(name) {
				continue
			}

			fileList.Files = append(fileList.Files, utils.SenderFile{
				Path:    "/",
				Regular: info.Mode().IsRegular(),
				WPath:   name,
			})

			// 1.   status byte (integer)
			_ = fec.WriteByte(flags)

			// 2.   inherited filename length (optional, byte)
			// 3.   filename length (integer or byte)
			fec.WriteInt32(int32(len(name)))

			// 4.   file (byte array)
			fec.WriteString(name)

			// 5.   file length (long)
			size := info.Size()
			if info.Mode().IsDir() {
				// tmpfs returns non-4K sizes for directories. Override with
				// 4096 to make the tests succeed regardless of the /tmp file
				// system type.
				size = 4096
			}
			fec.WriteInt64(size)

			fileList.TotalSize += size

			// 6.   file modification time (optional, integer)
			// TODO: this will overflow in 2038! :(
			fec.WriteInt32(int32(info.ModTime().Unix()))

			// 7.   file mode (optional, mode_t, integer)
			mode := int32(info.Mode() & os.ModePerm)
			isDev := false
			isSpecial := false
			if info.Mode().IsDir() {
				mode |= rsync.S_IFDIR
			} else if info.Mode().IsRegular() {
				mode |= rsync.S_IFREG
			} else if info.Mode().Type()&os.ModeSymlink != 0 {
				mode |= rsync.S_IFLNK
				// TODO: skip symlink if PreserveSymlinks is not set
			}

			if info.Mode().Type()&os.ModeCharDevice != 0 {
				mode |= rsync.S_IFCHR
				isDev = true
			} else if info.Mode().Type()&os.ModeDevice != 0 {
				mode |= rsync.S_IFBLK
				isDev = true
			}

			if info.Mode().Type()&os.ModeNamedPipe != 0 {
				mode |= rsync.S_IFIFO
				isSpecial = true
			}

			if info.Mode().Type()&os.ModeSocket != 0 {
				mode |= rsync.S_IFSOCK
				isSpecial = true
			}

			fec.WriteInt32(mode)

			if opts.PreserveUid() {
				uid, ok := uidFromFileInfo(info)
				if ok {
					if _, ok := uidMap[uid]; !ok && uid != 0 {
						u, err := user.LookupId(strconv.Itoa(int(uid)))
						if err != nil {
							lookupOnce.Do(func() {
								st.Logger.Error("lookup", "uid", uid, "err", err)
							})
						} else {
							uidMap[uid] = u.Username
						}
					}
				}
				// 8.   if -o, the user id (integer)
				fec.WriteInt32(uid)
			}

			if opts.PreserveGid() {
				gid, ok := gidFromFileInfo(info)
				if ok {
					if _, ok := gidMap[gid]; !ok && gid != 0 {
						g, err := user.LookupGroupId(strconv.Itoa(int(gid)))
						if err != nil {
							lookupGroupOnce.Do(func() {
								st.Logger.Error("lookupgroup", "gid", gid, "err", err)
							})
						} else {
							gidMap[gid] = g.Name
						}
					}
				}
				// 9.   if -g, the group id (integer)
				fec.WriteInt32(gid)
			}

			if (opts.PreserveDevices() && isDev) ||
				(opts.PreserveSpecials() && isSpecial) {
				// 10.  if a special file and -D, the device “rdev” type (integer)
				rdev, _ := rdevFromFileInfo(info)
				fec.WriteInt32(rdev)
			}

			if opts.PreserveLinks() && info.Mode().Type()&os.ModeSymlink != 0 {
				// 11.  if a symbolic link and -l, the link target's length (integer)
				// 12.  if a symbolic link and -l, the link target (byte array)
				target, err := os.Readlink(path)
				if err != nil {
					continue
				}
				fec.WriteInt32(int32(len(target)))
				fec.WriteString(target)
			}

			if opts.AlwaysChecksum() {
				var emptyChecksum [rsyncchecksum.Size]byte
				checksum := emptyChecksum[:]
				if info.Mode().IsRegular() {
					// TODO: send md4 checksum of this file
					checksum, err = rsyncchecksum.FileChecksum(path)
					if err != nil {
						continue
					}
				}
				// For non-regular files, send empty md4 checksum
				fec.WriteString(string(checksum))
			}
		}
		if err != nil {
			return nil, err
		}
	}

	const endOfFileList = 0
	_ = fec.WriteByte(endOfFileList)

	const endOfSet = 0
	if opts.PreserveUid() {
		for uid, name := range uidMap {
			fec.WriteInt32(uid)
			_ = fec.WriteByte(byte(len(name)))
			fec.WriteString(name)
		}
		fec.WriteInt32(endOfSet)
	}

	if opts.PreserveGid() {
		for gid, name := range gidMap {
			fec.WriteInt32(gid)
			_ = fec.WriteByte(byte(len(name)))
			fec.WriteString(name)
		}
		fec.WriteInt32(endOfSet)
	}

	const ioErrors = 0
	fec.WriteInt32(ioErrors)

	if err := st.Conn.WriteString(fec.String()); err != nil {
		return nil, err
	}

	return &fileList, nil
}
