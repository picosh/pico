package rsync

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

const (
	nameNormal = iota
	nameDotDir
)

// buildFileList turns the sender's source arguments into file-list entries
// (send_file_list). Errors about individual sources are reported to the
// client and recorded in s.ioError.
func (s *session) buildFileList(args []string) ([]*fileEntry, error) {
	var files []*fileEntry
	flags := 0
	if s.opts.recurse {
		flags = flagContentDir
	}
	for _, arg := range args {
		fbuf := strings.TrimLeft(arg, "/")
		nameType := nameNormal
		switch {
		case fbuf == "" || strings.HasSuffix(fbuf, "/"):
			if fbuf == "./" {
				fbuf = "."
			} else {
				fbuf += "."
			}
			nameType = nameDotDir
		case fbuf == "." || strings.HasSuffix(fbuf, "/."):
			nameType = nameDotDir
		}
		dir, fn := "", fbuf
		if i := strings.LastIndexByte(fbuf, '/'); i >= 0 {
			dir, fn = fbuf[:i], fbuf[i+1:]
		}
		if fn == "" {
			fn = "."
			nameType = nameDotDir
		}
		dir, err := cleanSourcePath(dir)
		if err != nil {
			return nil, err
		}
		if fn == ".." || strings.Contains(fn, "\x00") {
			return nil, fmt.Errorf("invalid source path: %s", arg)
		}
		src := dir
		if fn != "." {
			src = path.Join(dir, fn)
		}

		info, err := s.fs.Stat(src)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				s.xferError("link_stat %q failed: No such file or directory (2)", "/"+src)
			} else {
				s.ioError |= ioErrGeneral
				s.xferError("link_stat %q failed: %v", "/"+src, err)
			}
			continue
		}
		if nameType != nameDotDir && s.filters.excluded(fn, info.IsDir) {
			continue
		}
		if info.IsDir && !s.opts.xferDirs {
			s.info("skipping directory %s", fn)
			continue
		}

		if s.opts.recurse || (s.opts.xferDirs && nameType != nameNormal) {
			top := s.newEntry(fn, src, info, flagTopDir|flagContentDir|flags)
			files = append(files, top)
			if info.IsDir {
				children, err := s.listDir(fn, src, flags)
				if err != nil {
					s.ioError |= ioErrGeneral
					s.xferError("opendir %q failed: %v", "/"+src, err)
					continue
				}
				files = append(files, children...)
			}
		} else {
			files = append(files, s.newEntry(fn, src, info, flags))
		}
	}
	return files, nil
}

// listDir returns the entries below a source directory, with names under
// prefix. Directories missing from the listing are implied by their
// contents, as object stores don't keep them.
func (s *session) listDir(prefix, src string, flags int) ([]*fileEntry, error) {
	entries, err := s.fs.ReadDir(src, s.opts.recurse)
	if err != nil {
		return nil, err
	}
	join := func(name string) string {
		if prefix == "." {
			return name
		}
		return prefix + "/" + name
	}

	byName := make(map[string]FileInfo, len(entries))
	for _, e := range entries {
		name, err := cleanSourcePath(e.Name)
		if err != nil || name == "" {
			continue
		}
		if !s.opts.recurse && strings.Contains(name, "/") {
			continue
		}
		e.Name = name
		byName[name] = e
		if s.opts.recurse {
			for d := path.Dir(name); d != "."; d = path.Dir(d) {
				if _, ok := byName[d]; ok {
					break
				}
				byName[d] = FileInfo{Name: d, IsDir: true, ModTime: e.ModTime}
			}
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)

	var files []*fileEntry
	var excludedDirs []string
	for _, name := range names {
		e := byName[name]
		if slices.ContainsFunc(excludedDirs, func(d string) bool { return strings.HasPrefix(name, d+"/") }) {
			continue
		}
		full := join(name)
		if s.filters.excluded(full, e.IsDir) {
			if e.IsDir {
				excludedDirs = append(excludedDirs, name)
			}
			continue
		}
		files = append(files, s.newEntry(full, path.Join(src, name), e, flags))
	}
	return files, nil
}

func (s *session) newEntry(name, src string, info FileInfo, flags int) *fileEntry {
	f := newFileEntry(name)
	if name == "." {
		f.dirname, f.basename = "", "."
	}
	f.src = src
	f.mtime = info.ModTime.Unix()
	f.nsec = uint32(info.ModTime.Nanosecond())
	if info.ModTime.IsZero() {
		f.mtime, f.nsec = time.Now().Unix(), 0
	}
	if info.IsDir {
		f.mode = sIFDIR | 0o755
		f.size = 4096
		f.flags = flags
	} else {
		f.mode = sIFREG | 0o644
		f.size = info.Size
	}
	return f
}

// cleanSourcePath normalizes a path inside the storage, refusing anything
// that would leave it.
func cleanSourcePath(p string) (string, error) {
	var parts []string
	for _, c := range strings.Split(p, "/") {
		switch c {
		case "", ".":
		case "..":
			return "", fmt.Errorf("path escapes the transfer root: %s", p)
		default:
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, "/"), nil
}

// runSender is the server side of a download (do_server_sender).
func (s *session) runSender(args []string) error {
	start := time.Now()
	if err := s.recvFilterList(); err != nil {
		return err
	}

	srcs := args
	if len(srcs) > 0 {
		// The first argument is the "." the client uses as a placeholder
		// for the working directory.
		srcs = srcs[1:]
	}
	if len(srcs) == 0 && (s.opts.recurse || s.opts.xferDirs) {
		srcs = []string{"."}
	}

	files, err := s.buildFileList(srcs)
	if err != nil {
		return s.fatal(exitFileSelect, err)
	}
	if s.opts.alwaysChecksum {
		for _, f := range files {
			if f.isReg() {
				if f.sum, err = s.fileChecksum(f.src); err != nil {
					s.ioError |= ioErrGeneral
					s.xferError("failed to checksum %q: %v", "/"+f.src, err)
				}
			}
		}
	}
	buildTime := time.Since(start)

	start = time.Now()
	codec := s.newFlistCodec()
	var totalSize int64
	for _, f := range files {
		if err := codec.sendEntry(s.c, f); err != nil {
			return err
		}
		if f.isReg() || f.isLink() {
			totalSize += f.size
		}
	}
	ioError := s.ioError
	if s.opts.ignoreErrors {
		ioError = 0
	}
	if ioError != 0 && !s.safeFlist {
		if err := codec.sendEnd(s.c, 0); err != nil {
			return err
		}
	} else if err := codec.sendEnd(s.c, ioError); err != nil {
		return err
	}
	sortFileList(files, s.c.protocol, false)
	if err := s.sendIDLists(); err != nil {
		return err
	}
	if s.c.protocol < 30 {
		if err := s.c.writeInt32(ioError); err != nil {
			return err
		}
	} else if !s.safeFlist && ioError != 0 {
		if err := s.c.writeMsgInt(msgIOError, ioError); err != nil {
			return err
		}
	}
	if err := s.c.flush(); err != nil {
		return err
	}
	xferTime := time.Since(start)

	if len(files) == 0 {
		return s.finish()
	}

	if err := s.sendFiles(files); err != nil {
		return err
	}

	if err := s.c.writeVarlong30(s.c.bytesRead(), 3); err != nil {
		return err
	}
	if err := s.c.writeVarlong30(s.c.bytesWritten(), 3); err != nil {
		return err
	}
	if err := s.c.writeVarlong30(totalSize, 3); err != nil {
		return err
	}
	if s.c.protocol >= 29 {
		if err := s.c.writeVarlong30(max(buildTime.Milliseconds(), 1), 3); err != nil {
			return err
		}
		if err := s.c.writeVarlong30(xferTime.Milliseconds(), 3); err != nil {
			return err
		}
	}
	if err := s.readFinalGoodbye(true); err != nil {
		return err
	}
	return s.finish()
}

func (s *session) sendIDLists() error {
	if s.opts.numericIDs {
		return nil
	}
	for _, want := range []bool{s.opts.preserveUID, s.opts.preserveGID} {
		if !want {
			continue
		}
		// Every entry is owned by id 0, which is never listed by number.
		if err := s.c.writeVarint30(0); err != nil {
			return err
		}
		if s.xmitID0Names {
			if err := s.c.writeByte(4); err != nil {
				return err
			}
			if err := s.c.write([]byte("root")); err != nil {
				return err
			}
		}
	}
	return nil
}

// sendFiles answers the receiver's requests until it is done
// (send_files).
func (s *session) sendFiles(files []*fileEntry) error {
	phase := 0
	maxPhase := 1
	if s.c.protocol >= 29 {
		maxPhase = 2
	}
	savedIOError := s.ioError
	tok := newTokenSender(s.c, s.compression, s.compressLevel)
	for {
		req, err := s.readNdxAndAttrs(len(files))
		if err != nil {
			return err
		}
		if req.ndx == ndxDone {
			phase++
			if phase > maxPhase {
				break
			}
			if err := s.c.writeNdx(ndxDone); err != nil {
				return err
			}
			continue
		}
		f := files[req.ndx]
		if req.iflags&itemTransfer != 0 && !f.isReg() {
			return s.fatal(exitProtocol, fmt.Errorf("received request to transfer non-regular file: %d", req.ndx))
		}
		if req.iflags&itemTransfer == 0 {
			if err := s.writeNdxAndAttrs(req); err != nil {
				return err
			}
			continue
		}
		if phase == 2 {
			return s.fatal(exitProtocol, errors.New("got transfer request in phase 2"))
		}
		if s.opts.dryRun {
			if err := s.writeNdxAndAttrs(req); err != nil {
				return err
			}
			continue
		}

		head, sums, err := s.receiveSums()
		if err != nil {
			return s.fatal(exitProtocol, err)
		}
		if err := s.sendFile(f, req, head, sums, tok); err != nil {
			return err
		}
	}
	if s.ioError != savedIOError && s.c.protocol >= 30 {
		if err := s.c.writeMsgInt(msgIOError, s.ioError); err != nil {
			return err
		}
	}
	return s.c.writeNdx(ndxDone)
}

func (s *session) sendFile(f *fileEntry, req ndxRequest, head sumHead, sums []sumBuf, tok tokenSender) error {
	file, info, err := s.fs.Open(f.src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.ioError |= ioErrVanished
			s.warning("file has vanished: %q", "/"+f.src)
		} else {
			s.ioError |= ioErrGeneral
			s.xferError("send_files failed to open %q: %v", "/"+f.src, err)
		}
		if s.c.protocol >= 30 {
			return s.c.writeMsgInt(msgNoSend, req.ndx)
		}
		return nil
	}
	defer func() { _ = file.Close() }()

	if err := s.writeNdxAndAttrs(req); err != nil {
		return err
	}
	if err := s.c.writeSumHead(head); err != nil {
		return err
	}
	inplace := (s.inplacePartial && req.fnamecmpType == fnamecmpPartialDir) ||
		(s.opts.inplace && req.fnamecmpType == fnamecmpFname)
	readSize := max(3*int64(head.blength), maxMapSize)
	m := &matcher{
		head:    head,
		sums:    sums,
		cs:      &s.cs,
		tok:     tok,
		buf:     newMapFile(file, info.Size, readSize, head.blength),
		sum:     s.cs.newXferSum(),
		inplace: inplace,
	}
	if err := m.run(info.Size); err != nil {
		return err
	}
	sum := m.sum.Sum(nil)
	if m.buf.err != nil {
		s.ioError |= ioErrGeneral
		s.xferError("read errors mapping %q: %v", "/"+f.src, m.buf.err)
		// A checksum the receiver can't match makes it discard the file.
		allZero := !slices.ContainsFunc(sum, func(b byte) bool { return b != 0 })
		clear(sum)
		if allZero {
			sum[len(sum)-1]++
		}
	}
	s.stats.literal += m.literal
	s.stats.matched += m.matched
	return s.c.write(sum)
}

// receiveSums reads the block signatures the generator computed for its
// copy of a file.
func (s *session) receiveSums() (sumHead, []sumBuf, error) {
	head, err := s.c.readSumHead(s.cs.xfer.size())
	if err != nil {
		return head, nil, err
	}
	sums := make([]sumBuf, head.count)
	sum2 := make([]byte, int(head.count)*int(head.s2length))
	var offset int64
	for i := range sums {
		v, err := s.c.readInt32()
		if err != nil {
			return head, nil, err
		}
		b := sum2[i*int(head.s2length) : (i+1)*int(head.s2length)]
		if err := s.c.readFull(b); err != nil {
			return head, nil, err
		}
		l := head.blength
		if int32(i) == head.count-1 && head.remainder != 0 {
			l = head.remainder
		}
		sums[i] = sumBuf{offset: offset, len: l, sum1: uint32(v), sum2: b}
		offset += int64(l)
	}
	return head, sums, nil
}

func (s *session) fileChecksum(name string) ([]byte, error) {
	f, info, err := s.fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := s.cs.newFileSum()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, info.Size)); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
