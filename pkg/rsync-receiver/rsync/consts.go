package rsync

// rsync.h.
const (
	XMIT_TOP_DIR             = (1 << 0)
	XMIT_SAME_MODE           = (1 << 1)
	XMIT_EXTENDED_FLAGS      = (1 << 2)
	XMIT_SAME_RDEV_pre28     = XMIT_EXTENDED_FLAGS /* Only in protocols < 28 */
	XMIT_SAME_UID            = (1 << 3)
	XMIT_SAME_GID            = (1 << 4)
	XMIT_SAME_NAME           = (1 << 5)
	XMIT_LONG_NAME           = (1 << 6)
	XMIT_SAME_TIME           = (1 << 7)
	XMIT_SAME_RDEV_MAJOR     = (1 << 8)
	XMIT_HAS_IDEV_DATA       = (1 << 9)
	XMIT_SAME_DEV            = (1 << 10)
	XMIT_RDEV_MINOR_IS_SMALL = (1 << 11)
)

// as per /usr/include/bits/stat.h.
const (
	S_IFMT   = 0o0170000 // bits determining the file type
	S_IFDIR  = 0o0040000 // Directory
	S_IFCHR  = 0o0020000 // Character device
	S_IFBLK  = 0o0060000 // Block device
	S_IFREG  = 0o0100000 // Regular file
	S_IFIFO  = 0o0010000 // FIFO
	S_IFLNK  = 0o0120000 // Symbolic link
	S_IFSOCK = 0o0140000 // Socket
)

// ProtocolVersion defines the currently implemented rsync protocol
// version. Protocol version 27 seems to be the safest bet for wide
// compatibility: version 27 was introduced by rsync 2.6.0 (released 2004), and
// is supported by openrsync and rsyn.
const ProtocolVersion = 27
