package rsync

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Live flags of a file-list entry.
const (
	flagTopDir     = 1 << 0
	flagContentDir = 1 << 2
	flagImpliedDir = 1 << 6
)

// fileEntry is one file-list entry.
type fileEntry struct {
	dirname  string // "" for entries at the top
	basename string
	mode     uint32 // unix mode including type bits
	size     int64
	mtime    int64
	nsec     uint32
	uid      uint32
	gid      uint32
	link     string
	sum      []byte // whole-file checksum with --checksum
	flags    int
	inactive bool // removed as a duplicate

	// sender only: where to read the file from
	src string
}

func (f *fileEntry) name() string {
	if f.dirname == "" {
		return f.basename
	}
	return f.dirname + "/" + f.basename
}

func (f *fileEntry) isDir() bool  { return f.mode&sIFMT == sIFDIR }
func (f *fileEntry) isReg() bool  { return f.mode&sIFMT == sIFREG }
func (f *fileEntry) isLink() bool { return f.mode&sIFMT == sIFLNK }
func (f *fileEntry) isDevice() bool {
	t := f.mode & sIFMT
	return t == sIFCHR || t == sIFBLK
}
func (f *fileEntry) isSpecial() bool {
	t := f.mode & sIFMT
	return t == sIFIFO || t == sIFSOCK
}

func newFileEntry(name string) *fileEntry {
	dir, base := path.Split(name)
	return &fileEntry{dirname: strings.TrimSuffix(dir, "/"), basename: base}
}

// flistCodec holds the state and options shared by the encoder and decoder.
// Both compress each entry against the previous one.
type flistCodec struct {
	protocol      int
	varintFlags   bool
	safeFlist     bool
	preserveUID   bool
	preserveGID   bool
	preserveLinks bool
	preserveDevs  bool
	preserveSpecs bool
	preserveAtime bool
	alwaysSum     bool
	sumLen        int

	last      fileEntry
	lastName  string
	lastAtime int64
	rdevMaj   uint32
}

func (fc *flistCodec) sendEntry(c *conn, f *fileEntry) error {
	name := f.name()
	var xflags int
	switch {
	case f.isDir():
		if fc.protocol >= 30 {
			switch {
			case f.flags&flagContentDir != 0:
				xflags = f.flags & flagTopDir
			case f.flags&flagImpliedDir != 0:
				xflags = xmitTopDir | xmitNoContentDir
			default:
				xflags = xmitNoContentDir
			}
		} else {
			xflags = f.flags & flagTopDir
		}
	}
	if f.mode == fc.last.mode && fc.lastName != "" {
		xflags |= xmitSameMode
	}
	if !fc.preserveUID || (f.uid == fc.last.uid && fc.lastName != "") {
		xflags |= xmitSameUID
	}
	if !fc.preserveGID || (f.gid == fc.last.gid && fc.lastName != "") {
		xflags |= xmitSameGID
	}
	if f.mtime == fc.last.mtime && fc.lastName != "" {
		xflags |= xmitSameTime
	}
	if f.nsec != 0 && fc.protocol >= 31 {
		xflags |= xmitModNsec
	}
	// We don't track access times, so the modification time stands in.
	atime := f.mtime
	if fc.preserveAtime && !f.isDir() {
		if atime == fc.lastAtime {
			xflags |= xmitSameAtime
		} else {
			fc.lastAtime = atime
		}
	}
	if fc.preserveSpecs && f.isSpecial() && fc.protocol < 31 {
		if fc.protocol < 28 {
			xflags |= xmitSameRdevPre28
		} else {
			xflags |= xmitSameRdevMajor
			if fc.protocol < 30 {
				xflags |= xmitRdevMinor8Pre30
			}
		}
	}

	l1 := 0
	for l1 < len(fc.lastName) && l1 < len(name) && l1 < 255 && name[l1] == fc.lastName[l1] {
		l1++
	}
	l2 := len(name) - l1
	if l1 > 0 {
		xflags |= xmitSameName
	}
	if l2 > 255 {
		xflags |= xmitLongName
	}

	switch {
	case fc.varintFlags:
		if xflags == 0 {
			xflags = xmitExtendedFlags
		}
		if err := c.writeVarint(int32(xflags)); err != nil {
			return err
		}
	case fc.protocol >= 28:
		if xflags == 0 && !f.isDir() {
			xflags |= xmitTopDir
		}
		if xflags&0xFF00 != 0 || xflags == 0 {
			xflags |= xmitExtendedFlags
			if err := c.writeShortint(uint16(xflags)); err != nil {
				return err
			}
		} else if err := c.writeByte(byte(xflags)); err != nil {
			return err
		}
	default:
		if xflags&0xFF == 0 {
			if f.isDir() {
				xflags |= xmitLongName
			} else {
				xflags |= xmitTopDir
			}
		}
		if err := c.writeByte(byte(xflags)); err != nil {
			return err
		}
	}
	if xflags&xmitSameName != 0 {
		if err := c.writeByte(byte(l1)); err != nil {
			return err
		}
	}
	if xflags&xmitLongName != 0 {
		if err := c.writeVarint30(int32(l2)); err != nil {
			return err
		}
	} else if err := c.writeByte(byte(l2)); err != nil {
		return err
	}
	if err := c.write([]byte(name[l1:])); err != nil {
		return err
	}

	if err := c.writeVarlong30(f.size, 3); err != nil {
		return err
	}
	if xflags&xmitSameTime == 0 {
		var err error
		if fc.protocol >= 30 {
			err = c.writeVarlong(f.mtime, 4)
		} else {
			err = c.writeInt32(int32(f.mtime))
		}
		if err != nil {
			return err
		}
	}
	if xflags&xmitModNsec != 0 {
		if err := c.writeVarint(int32(f.nsec)); err != nil {
			return err
		}
	}
	if xflags&xmitSameMode == 0 {
		if err := c.writeInt32(int32(f.mode)); err != nil {
			return err
		}
	}
	if fc.preserveAtime && !f.isDir() && xflags&xmitSameAtime == 0 {
		if err := c.writeVarlong(atime, 4); err != nil {
			return err
		}
	}
	if fc.preserveUID && xflags&xmitSameUID == 0 {
		if err := c.writeVarint30(int32(f.uid)); err != nil {
			return err
		}
	}
	if fc.preserveGID && xflags&xmitSameGID == 0 {
		if err := c.writeVarint30(int32(f.gid)); err != nil {
			return err
		}
	}
	if fc.preserveSpecs && f.isSpecial() && fc.protocol < 31 {
		// The rdev of a special file is meaningless, so it is always
		// sent as "same major" with a zero minor.
		switch {
		case fc.protocol >= 30:
			if err := c.writeVarint(0); err != nil {
				return err
			}
		case fc.protocol >= 28:
			if err := c.writeByte(0); err != nil {
				return err
			}
		}
	}
	if fc.preserveLinks && f.isLink() {
		if err := c.writeVarint30(int32(len(f.link))); err != nil {
			return err
		}
		if err := c.write([]byte(f.link)); err != nil {
			return err
		}
	}
	if fc.alwaysSum && (f.isReg() || fc.protocol < 28) {
		sum := f.sum
		if !f.isReg() || len(sum) != fc.sumLen {
			sum = make([]byte, fc.sumLen)
		}
		if err := c.write(sum); err != nil {
			return err
		}
	}

	fc.last = *f
	fc.lastName = name
	return nil
}

// sendEnd terminates the file list.
func (fc *flistCodec) sendEnd(c *conn, ioError int32) error {
	switch {
	case fc.varintFlags:
		if err := c.writeVarint(0); err != nil {
			return err
		}
		return c.writeVarint(ioError)
	case ioError != 0 && fc.safeFlist:
		if err := c.writeShortint(xmitExtendedFlags | xmitIOErrorEndList); err != nil {
			return err
		}
		return c.writeVarint(ioError)
	default:
		return c.writeByte(0)
	}
}

// recvFlags reads the flags of the next entry. It returns ok=false at the end
// of the list, along with any I/O error the sender reported there.
func (fc *flistCodec) recvFlags(c *conn) (xflags int, ok bool, ioError int32, err error) {
	if fc.varintFlags {
		v, err := c.readVarint()
		if err != nil {
			return 0, false, 0, err
		}
		if v == 0 {
			e, err := c.readVarint()
			return 0, false, e, err
		}
		return int(v), true, 0, nil
	}
	b, err := c.readByte()
	if err != nil {
		return 0, false, 0, err
	}
	if b == 0 {
		return 0, false, 0, nil
	}
	xflags = int(b)
	if fc.protocol >= 28 && xflags&xmitExtendedFlags != 0 {
		hi, err := c.readByte()
		if err != nil {
			return 0, false, 0, err
		}
		xflags |= int(hi) << 8
	}
	if xflags == xmitExtendedFlags|xmitIOErrorEndList {
		if !fc.safeFlist {
			return 0, false, 0, fmt.Errorf("invalid flist flag: %x", xflags)
		}
		e, err := c.readVarint()
		return 0, false, e, err
	}
	return xflags, true, 0, nil
}

func (fc *flistCodec) recvEntry(c *conn, xflags int) (*fileEntry, error) {
	var l1, l2 int
	if xflags&xmitSameName != 0 {
		b, err := c.readByte()
		if err != nil {
			return nil, err
		}
		l1 = int(b)
	}
	if xflags&xmitLongName != 0 {
		v, err := c.readVarint30()
		if err != nil {
			return nil, err
		}
		l2 = int(v)
	} else {
		b, err := c.readByte()
		if err != nil {
			return nil, err
		}
		l2 = int(b)
	}
	if l1 > len(fc.lastName) || l2 < 0 || l2 >= maxPathLen-l1 {
		return nil, fmt.Errorf("overflow: xflags=0x%x l1=%d l2=%d lastname=%s", xflags, l1, l2, fc.lastName)
	}
	nameBuf := make([]byte, l1+l2)
	copy(nameBuf, fc.lastName[:l1])
	if err := c.readFull(nameBuf[l1:]); err != nil {
		return nil, err
	}
	rawName := string(nameBuf)
	fc.lastName = rawName

	name, err := cleanName(rawName)
	if err != nil {
		return nil, err
	}
	f := newFileEntry(name)
	if name == "." {
		f.dirname, f.basename = "", "."
	}

	if xflags&(xmitHlinked) != 0 && fc.protocol >= 30 && xflags&xmitHlinkFirst == 0 {
		return nil, errors.New("hard links are not supported")
	}

	if f.size, err = c.readVarlong30(3); err != nil {
		return nil, err
	}
	if f.size < 0 {
		return nil, errors.New("offset underflow: file-length is negative")
	}
	if xflags&xmitSameTime != 0 {
		f.mtime = fc.last.mtime
	} else if fc.protocol >= 30 {
		if f.mtime, err = c.readVarlong(4); err != nil {
			return nil, err
		}
	} else {
		v, err := c.readInt32()
		if err != nil {
			return nil, err
		}
		f.mtime = int64(uint32(v))
	}
	if xflags&xmitModNsec != 0 {
		v, err := c.readVarint()
		if err != nil {
			return nil, err
		}
		if v < 0 || v > maxWireNsec {
			return nil, fmt.Errorf("invalid modtime nsec %d", v)
		}
		f.nsec = uint32(v)
	}
	if xflags&xmitSameMode != 0 {
		f.mode = fc.last.mode
	} else {
		v, err := c.readInt32()
		if err != nil {
			return nil, err
		}
		f.mode = uint32(v)
		switch f.mode & sIFMT {
		case sIFREG, sIFDIR, sIFLNK, sIFCHR, sIFBLK, sIFIFO, sIFSOCK:
		default:
			return nil, fmt.Errorf("invalid file mode 0%o for %s", f.mode, name)
		}
	}
	if fc.preserveAtime && !f.isDir() && xflags&xmitSameAtime == 0 {
		if fc.lastAtime, err = c.readVarlong(4); err != nil {
			return nil, err
		}
	}
	f.uid, f.gid = fc.last.uid, fc.last.gid
	if fc.preserveUID && xflags&xmitSameUID == 0 {
		v, err := c.readVarint30()
		if err != nil {
			return nil, err
		}
		f.uid = uint32(v)
		if fc.protocol >= 30 && xflags&xmitUserNameFollows != 0 {
			if _, err := fc.readIDName(c); err != nil {
				return nil, err
			}
		}
	}
	if fc.preserveGID && xflags&xmitSameGID == 0 {
		v, err := c.readVarint30()
		if err != nil {
			return nil, err
		}
		f.gid = uint32(v)
		if fc.protocol >= 30 && xflags&xmitGroupNameFollows != 0 {
			if _, err := fc.readIDName(c); err != nil {
				return nil, err
			}
		}
	}
	if (fc.preserveDevs && f.isDevice()) || (fc.preserveSpecs && f.isSpecial() && fc.protocol < 31) {
		if err := fc.skipRdev(c, xflags); err != nil {
			return nil, err
		}
		f.size = 0
	} else if f.isDevice() {
		f.size = 0
	}
	if fc.preserveLinks && f.isLink() {
		n, err := c.readVarint30()
		if err != nil {
			return nil, err
		}
		if n < 0 || n >= maxPathLen {
			return nil, fmt.Errorf("overflow: linkname_len=%d", n)
		}
		link := make([]byte, n)
		if err := c.readFull(link); err != nil {
			return nil, err
		}
		f.link = string(link)
	}
	if xflags&xmitHlinked != 0 && fc.protocol < 30 {
		return nil, errors.New("hard links are not supported")
	}
	if fc.alwaysSum && (f.isReg() || fc.protocol < 28) {
		sum := make([]byte, fc.sumLen)
		if err := c.readFull(sum); err != nil {
			return nil, err
		}
		if f.isReg() {
			f.sum = sum
		}
	}

	if name == "." && !f.isDir() {
		return nil, fmt.Errorf("rejecting non-directory transfer-root entry: %s", name)
	}
	if f.isDir() {
		if fc.protocol >= 30 {
			if xflags&xmitNoContentDir == 0 {
				if xflags&xmitTopDir != 0 {
					f.flags |= flagTopDir
				}
				f.flags |= flagContentDir
			} else if xflags&xmitTopDir != 0 {
				f.flags |= flagImpliedDir
			}
		} else if xflags&xmitTopDir != 0 {
			f.flags |= flagTopDir | flagContentDir
		}
	}

	fc.last = *f
	return f, nil
}

func (fc *flistCodec) readIDName(c *conn) (string, error) {
	n, err := c.readByte()
	if err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if err := c.readFull(buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func (fc *flistCodec) skipRdev(c *conn, xflags int) error {
	if fc.protocol < 28 {
		if xflags&xmitSameRdevPre28 == 0 {
			_, err := c.readInt32()
			return err
		}
		return nil
	}
	if xflags&xmitSameRdevMajor == 0 {
		v, err := c.readVarint30()
		if err != nil {
			return err
		}
		fc.rdevMaj = uint32(v)
	}
	var err error
	switch {
	case fc.protocol >= 30:
		_, err = c.readVarint()
	case xflags&xmitRdevMinor8Pre30 != 0:
		_, err = c.readByte()
	default:
		_, err = c.readInt32()
	}
	return err
}

// cleanName validates a name received from the peer and normalizes it the
// way clean_fname does: collapse slashes, drop "." components, refuse "..".
func cleanName(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty file name from sender")
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("ABORTING due to unsafe pathname from sender: %s", name)
	}
	var parts []string
	for _, p := range strings.Split(name, "/") {
		switch p {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("ABORTING due to unsafe pathname from sender: %s", name)
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return ".", nil
	}
	return strings.Join(parts, "/"), nil
}

// Name-comparison states and types for fileNameCmp.
const (
	sDir = iota
	sSlash
	sBase
	sTrailing
)

const (
	tPath = iota
	tItem
)

// fileNameCmp orders entries the way rsync's f_name_cmp does. Both sides
// sort the list independently and address files by index, so this has to
// match byte for byte. From protocol 29 on, a directory sorts after the
// files beside it, as if its name ended in "/".
func fileNameCmp(f1, f2 *fileEntry, protocol int) int {
	if f1.inactive {
		if f2.inactive {
			return 0
		}
		return -1
	}
	if f2.inactive {
		return 1
	}
	tp := tItem
	if protocol >= 29 {
		tp = tPath
	}

	a := newNameCursor(f1, tp)
	b := newNameCursor(f2, tp)
	if f1.dirname == f2.dirname {
		a.atBase()
		b.atBase()
	}
	if a.typ != b.typ {
		if a.typ == tPath {
			return 1
		}
		return -1
	}

	for {
		if a.empty() {
			a.advance()
			if !b.empty() && a.typ != b.typ {
				if a.typ == tPath {
					return 1
				}
				return -1
			}
		}
		if b.empty() {
			if b.state == sTrailing {
				if a.empty() {
					return 0
				}
				b.typ = tItem
			} else {
				b.advance()
			}
			if a.typ != b.typ {
				if a.typ == tPath {
					return 1
				}
				return -1
			}
		}
		ca, cb := a.next(), b.next()
		if ca != cb {
			return int(ca) - int(cb)
		}
		if ca == 0 {
			// Both strings ended in the trailing state.
			return 0
		}
	}
}

type nameCursor struct {
	f     *fileEntry
	tp    int
	s     string
	i     int
	state int
	typ   int
}

func newNameCursor(f *fileEntry, tp int) *nameCursor {
	c := &nameCursor{f: f, tp: tp}
	if f.dirname == "" {
		c.atBase()
	} else {
		c.s = f.dirname
		c.typ = tp
		c.state = sDir
	}
	return c
}

func (c *nameCursor) atBase() {
	c.typ = tItem
	if c.f.isDir() {
		c.typ = c.tp
	}
	c.s, c.i = c.f.basename, 0
	if c.typ == tPath && c.s == "." {
		c.typ = tItem
		c.state = sTrailing
		c.s = ""
	} else {
		c.state = sBase
	}
}

func (c *nameCursor) empty() bool { return c.i >= len(c.s) }

// advance moves past the end of the current string the way the C state
// machine does when it hits a NUL.
func (c *nameCursor) advance() {
	switch c.state {
	case sDir:
		c.state = sSlash
		c.s, c.i = "/", 0
	case sSlash:
		c.atBase()
	case sBase:
		c.state = sTrailing
		if c.typ == tPath {
			c.s, c.i = "/", 0
			return
		}
		c.typ = tItem
	case sTrailing:
		c.typ = tItem
	}
}

func (c *nameCursor) next() byte {
	if c.i >= len(c.s) {
		return 0
	}
	b := c.s[c.i]
	c.i++
	return b
}

// sortFileList sorts the list and, on the receiving side, marks duplicate
// names inactive without disturbing the indexes (flist_sort_and_clean).
func sortFileList(files []*fileEntry, protocol int, receiver bool) {
	slices.SortStableFunc(files, func(a, b *fileEntry) int {
		return fileNameCmp(a, b, protocol)
	})
	if !receiver {
		return
	}
	seen := make(map[string]int, len(files))
	for i, f := range files {
		if f.inactive {
			continue
		}
		name := f.name()
		j, dup := seen[name]
		if !dup {
			seen[name] = i
			continue
		}
		// Keep a directory over a non-directory, otherwise the first.
		prev := files[j]
		switch {
		case f.isDir() && !prev.isDir():
			prev.inactive = true
			seen[name] = i
		case f.isDir():
			prev.flags |= f.flags & (flagTopDir | flagContentDir)
			prev.flags &= f.flags | ^flagImpliedDir
			f.inactive = true
		default:
			f.inactive = true
		}
	}
}
