package rsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path"
	"slices"
	"strings"
	"time"
)

var errChecksumMismatch = errors.New("rsync: file failed verification")

// recvEvent is what the receiver tells the generator, standing in for the
// pipe between rsync's two receiving processes.
type recvEvent struct {
	done bool
	redo int32
}

// destination maps file-list names to storage paths (get_local_name).
type destination struct {
	dir    string
	single bool // the transfer is one file written to dir itself
}

func (d destination) path(f *fileEntry) string {
	if d.single {
		return d.dir
	}
	name := f.name()
	if name == "." {
		return d.dir
	}
	return path.Join(d.dir, name)
}

// runReceiver is the server side of an upload (do_server_recv).
func (s *session) runReceiver(args []string) error {
	if err := s.recvFilterList(); err != nil {
		return err
	}
	files, err := s.recvFileList()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return s.fatal(exitFileSelect, errors.New("server_recv: recv_file_list error"))
	}

	base := ""
	if len(args) > 0 {
		base = args[0]
	}
	destArg := ""
	if len(args) > 1 {
		destArg = args[1]
	}
	dest, err := s.resolveDestination(files, base, destArg)
	if err != nil {
		return s.fatal(exitFileSelect, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan recvEvent, 64)
	g := &generator{s: s, files: files, dest: dest, events: events, ctx: ctx}
	r := &receiver{s: s, files: files, dest: dest, events: events, ctx: ctx, sent: make([]bool, len(files))}

	errc := make(chan error, 2)
	go func() { errc <- g.run() }()
	go func() { errc <- r.run() }()
	for range 2 {
		if err := <-errc; err != nil {
			cancel()
			return err
		}
	}
	return s.finish()
}

func (s *session) recvFileList() ([]*fileEntry, error) {
	codec := s.newFlistCodec()
	var files []*fileEntry
	for {
		xflags, ok, ioErr, err := codec.recvFlags(s.c)
		if err != nil {
			return nil, err
		}
		if !ok {
			if !s.opts.ignoreErrors {
				s.addIOError(ioErr & ioErrMask)
			}
			break
		}
		f, err := codec.recvEntry(s.c, xflags)
		if err != nil {
			return nil, s.fatal(exitProtocol, err)
		}
		files = append(files, f)
	}
	if !s.opts.numericIDs {
		for _, want := range []bool{s.opts.preserveUID, s.opts.preserveGID} {
			if !want {
				continue
			}
			for {
				id, err := s.c.readVarint30()
				if err != nil {
					return nil, err
				}
				if id == 0 {
					break
				}
				if _, err := codec.readIDName(s.c); err != nil {
					return nil, err
				}
			}
			if s.xmitID0Names {
				if _, err := codec.readIDName(s.c); err != nil {
					return nil, err
				}
			}
		}
	}
	sortFileList(files, s.c.protocol, true)
	if s.c.protocol < 30 {
		v, err := s.c.readInt32()
		if err != nil {
			return nil, err
		}
		if !s.opts.ignoreErrors {
			s.addIOError(v & ioErrMask)
		}
	}
	return files, nil
}

func (s *session) resolveDestination(files []*fileEntry, base, destArg string) (destination, error) {
	dir, err := cleanSourcePath(strings.TrimLeft(path.Join(base, destArg), "/"))
	if err != nil {
		return destination{}, err
	}
	trailingSlash := strings.HasSuffix(destArg, "/")
	if dir == "" {
		return destination{dir: dir}, nil
	}
	info, err := s.fs.Stat(dir)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return destination{}, fmt.Errorf("ERROR: cannot stat destination %q: %w", "/"+dir, err)
	}
	if exists {
		if info.IsDir {
			return destination{dir: dir}, nil
		}
		if len(files) > 1 {
			return destination{}, errors.New("ERROR: destination must be a directory when copying more than 1 file")
		}
		if files[0].isDir() {
			return destination{}, errors.New("ERROR: cannot overwrite non-directory with a directory")
		}
	}
	if len(files) > 1 || trailingSlash {
		if exists {
			return destination{}, errors.New("ERROR: destination path is not a directory")
		}
		return destination{dir: dir}, nil
	}
	return destination{dir: dir, single: true}, nil
}

// generator decides what the client has to send and sends the signatures
// of our copies (generator.c).
type generator struct {
	s      *session
	files  []*fileEntry
	dest   destination
	events chan recvEvent
	ctx    context.Context
	done   int

	deletedFiles int32
	deletedDirs  int32
}

func (g *generator) run() error {
	s := g.s
	c := s.c
	earlyDelayDone := !s.opts.delayUpdates
	earlyDeleteDone := s.opts.deleteDuring != 2 && !s.opts.deleteAfter

	if s.opts.deleteMode && !g.dest.single && (s.opts.deleteBefore || s.opts.deleteDuring == 1) {
		if err := g.deletePass(); err != nil {
			return err
		}
	}

	for i, f := range g.files {
		if f.inactive {
			continue
		}
		if err := g.ctx.Err(); err != nil {
			return err
		}
		if err := g.recvGenerator(int32(i), f, false); err != nil {
			return err
		}
	}
	if err := c.writeNdx(ndxDone); err != nil {
		return err
	}

	// Redo files that failed verification until the receiver is done
	// with the first pass.
	if err := g.wait(1, true); err != nil {
		return err
	}

	if err := c.writeNdx(ndxDone); err != nil {
		return err
	}
	if c.protocol >= 29 && earlyDelayDone {
		if err := c.writeNdx(ndxDone); err != nil {
			return err
		}
	}
	if c.protocol >= 31 && earlyDeleteDone {
		if s.opts.deleteMode {
			if err := g.writeDelStats(); err != nil {
				return err
			}
		}
		if earlyDelayDone {
			if err := c.writeNdx(ndxDone); err != nil {
				return err
			}
		}
	}
	if err := g.wait(2, false); err != nil {
		return err
	}

	if c.protocol >= 29 {
		if !earlyDelayDone {
			if err := c.writeNdx(ndxDone); err != nil {
				return err
			}
			if c.protocol >= 31 && earlyDeleteDone {
				if err := c.writeNdx(ndxDone); err != nil {
					return err
				}
			}
		}
		if err := g.wait(3, false); err != nil {
			return err
		}
	}

	if s.opts.deleteMode && !g.dest.single && (s.opts.deleteDuring == 2 || s.opts.deleteAfter) {
		if err := g.deletePass(); err != nil {
			return err
		}
	}

	if c.protocol >= 31 {
		if !earlyDeleteDone {
			if s.opts.deleteMode {
				if err := g.writeDelStats(); err != nil {
					return err
				}
			}
			if err := c.writeNdx(ndxDone); err != nil {
				return err
			}
		}
		if err := g.wait(4, false); err != nil {
			return err
		}
	}

	// The final goodbye.
	if err := c.writeNdx(ndxDone); err != nil {
		return err
	}
	return c.flush()
}

// wait handles receiver events until it has finished n phases.
func (g *generator) wait(n int, redo bool) error {
	if err := g.s.c.w.Kick(); err != nil {
		return err
	}
	for g.done < n {
		select {
		case <-g.ctx.Done():
			return g.ctx.Err()
		case ev := <-g.events:
			if ev.done {
				g.done++
				continue
			}
			if redo {
				if err := g.recvGenerator(ev.redo, g.files[ev.redo], true); err != nil {
					return err
				}
				if err := g.s.c.w.Kick(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// recvGenerator handles one file-list entry (generator.c:recv_generator).
// A redo asks again for a file that failed verification, with full-length
// block checksums.
func (g *generator) recvGenerator(ndx int32, f *fileEntry, redo bool) error {
	s := g.s
	o := &s.opts
	if !f.isReg() {
		if !f.isDir() && o.infoNonreg > 0 {
			s.info("skipping non-regular file %q", f.name())
		}
		return nil
	}
	if !redo {
		if o.maxSize >= 0 && f.size > o.maxSize {
			return nil
		}
		if o.minSize >= 0 && f.size < o.minSize {
			return nil
		}
	}

	target := g.dest.path(f)
	st, err := s.fs.Stat(target)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.xferError("recv_generator: failed to stat %q: %v", "/"+target, err)
		return nil
	}
	if !redo {
		if o.ignoreNonExist && !exists {
			return nil
		}
		if o.ignoreExisting && exists {
			return nil
		}
		if o.updateOnly && exists && !st.IsDir && f.mtime-st.ModTime.Unix() < 0 {
			return nil
		}
	}
	if exists && st.IsDir {
		s.xferError("cannot replace directory %q with a file", "/"+target)
		return nil
	}

	if exists && !redo {
		same, err := g.quickCheck(f, target, st)
		if err != nil {
			s.xferError("failed to checksum %q: %v", "/"+target, err)
		} else if same {
			if o.removeSource && !o.dryRun {
				return s.c.writeMsgInt(msgSuccess, ndx)
			}
			return nil
		}
	}

	// Build the signature of our copy before announcing the file, so a
	// failure to read it can fall back to sending the whole file.
	var head sumHead
	var sums []byte
	if exists && !o.wholeFile && !o.dryRun && st.Size > 0 {
		csumLength := shortSumLength
		if redo {
			csumLength = sumLength
		}
		head, sums, err = g.signature(target, csumLength)
		if err != nil {
			s.send(msgError, fmt.Sprintf("rsync: [generator] failed to read %q, continuing: %v\n", "/"+target, err))
			exists = false
			head, sums = sumHead{}, nil
		}
	}

	if err := s.c.writeNdx(ndx); err != nil {
		return err
	}
	if s.c.protocol >= 29 {
		iflags := itemTransfer
		if o.alwaysChecksum {
			iflags |= itemReportChange
		}
		if exists {
			if st.Size != f.size {
				iflags |= itemReportSize
			}
			if !o.preserveTimes || st.ModTime.Unix() != f.mtime {
				iflags |= itemReportTime
			}
		} else {
			iflags |= itemIsNew
		}
		if err := s.c.writeShortint(uint16(iflags)); err != nil {
			return err
		}
	}
	if o.dryRun {
		return nil
	}
	if err := s.c.writeSumHead(head); err != nil {
		return err
	}
	return s.c.write(sums)
}

// signature computes the block checksums of our copy of a file.
func (g *generator) signature(target string, csumLength int) (sumHead, []byte, error) {
	s := g.s
	basis, info, err := s.fs.Open(target)
	if err != nil {
		return sumHead{}, nil, err
	}
	defer func() { _ = basis.Close() }()
	if info.Size <= 0 {
		return sumHead{}, nil, nil
	}
	head, err := sumSizesSqroot(info.Size, s.c.protocol, s.opts.blockSize, csumLength, s.cs.xfer.size())
	if err != nil {
		return sumHead{}, nil, err
	}
	sums, err := encodeSums(basis, info.Size, head, &s.cs)
	if err != nil {
		return sumHead{}, nil, err
	}
	return head, sums, nil
}

// quickCheck reports whether our copy already matches (quick_check_ok).
func (g *generator) quickCheck(f *fileEntry, target string, st FileInfo) (bool, error) {
	o := &g.s.opts
	if st.Size != f.size {
		return false, nil
	}
	if o.alwaysChecksum {
		sum, err := g.s.fileChecksum(target)
		if err != nil {
			return false, err
		}
		return bytes.Equal(sum, f.sum), nil
	}
	if o.sizeOnly {
		return true, nil
	}
	if o.ignoreTimes {
		return false, nil
	}
	// Storage keeps whole seconds, so nanoseconds are not compared.
	return st.ModTime.Unix() == f.mtime, nil
}

func (g *generator) writeDelStats() error {
	return g.s.writeDelStats([5]int32{g.deletedFiles, g.deletedDirs, 0, 0, 0})
}

// deletePass removes what the destination has beyond the file list.
func (g *generator) deletePass() error {
	s := g.s
	s.mu.Lock()
	ioError := s.ioError
	s.mu.Unlock()
	if ioError != 0 && !s.opts.ignoreErrors {
		s.info("IO error encountered -- skipping file deletion")
		return nil
	}

	existing, err := s.fs.ReadDir(g.dest.dir, true)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.xferError("delete: failed to list %q: %v", "/"+g.dest.dir, err)
		return nil
	}

	listed := make(map[string]*fileEntry, len(g.files))
	for _, f := range g.files {
		if !f.inactive {
			listed[f.name()] = f
		}
	}
	contentDir := func(name string) bool {
		f, ok := listed[name]
		if !ok || !f.isDir() {
			return false
		}
		if g.s.c.protocol < 30 && s.opts.recurse {
			return true
		}
		return f.flags&flagContentDir != 0
	}

	type victim struct {
		name  string
		isDir bool
	}
	var victims []victim
	var protectedNames []string
	for _, e := range existing {
		name, err := cleanSourcePath(e.Name)
		if err != nil || name == "" {
			continue
		}
		if f, ok := listed[name]; ok && f.isDir() == e.IsDir {
			continue
		}
		// Find the closest listed ancestor. Everything between it and
		// the entry is unlisted and goes too, unless a filter protects
		// part of the way.
		anc := path.Dir(name)
		var chain []string
		chain = append(chain, name)
		for anc != "." {
			if _, ok := listed[anc]; ok {
				break
			}
			chain = append(chain, anc)
			anc = path.Dir(anc)
		}
		if !contentDir(anc) {
			continue
		}
		protected := false
		for i, p := range chain {
			if s.filters.excluded(p, i > 0 || e.IsDir) {
				protected = true
				break
			}
		}
		if protected {
			protectedNames = append(protectedNames, name)
		} else {
			victims = append(victims, victim{name: name, isDir: e.IsDir})
		}
	}

	// Children before parents.
	slices.SortStableFunc(victims, func(a, b victim) int {
		return strings.Count(b.name, "/") - strings.Count(a.name, "/")
	})

	maxDelete := s.opts.maxDelete
	if maxDelete < 0 && maxDelete != math.MinInt32 {
		maxDelete = 0
	}
	skipped := 0
	for _, v := range victims {
		if maxDelete >= 0 && int(g.deletedFiles+g.deletedDirs) >= maxDelete {
			skipped++
			continue
		}
		if v.isDir && slices.ContainsFunc(protectedNames, func(p string) bool { return strings.HasPrefix(p, v.name+"/") }) {
			s.info("cannot delete non-empty directory: %s", v.name)
			continue
		}
		full := path.Join(g.dest.dir, v.name)
		if !s.opts.dryRun {
			if err := s.fs.Remove(full, v.isDir); err != nil {
				op := "unlink"
				if v.isDir {
					op = "rmdir"
				}
				s.xferError("delete_file: %s(%s) failed: %v", op, v.name, err)
				continue
			}
		}
		if v.isDir {
			g.deletedDirs++
		} else {
			g.deletedFiles++
		}
		g.logDelete(v.name, v.isDir)
	}
	if skipped > 0 {
		s.warning("Deletions stopped due to --max-delete limit (%d skipped)", skipped)
		s.addIOError(ioErrDelLimit)
	}
	return nil
}

func (g *generator) logDelete(name string, isDir bool) {
	s := g.s
	if s.c.protocol >= 29 {
		msg := []byte(name)
		if isDir {
			msg = append(msg, 0)
		}
		if err := s.c.writeMsg(msgDeleted, msg); err != nil {
			s.logger.Debug("rsync message not delivered", "err", err)
		}
		return
	}
	if s.opts.infoDel > 0 {
		if isDir {
			name += "/"
		}
		s.info("deleting %s", name)
	}
}

// receiver applies the deltas the client sends (receiver.c).
type receiver struct {
	s      *session
	files  []*fileEntry
	dest   destination
	events chan recvEvent
	ctx    context.Context
	sent   []bool
}

func (r *receiver) notify(ev recvEvent) error {
	select {
	case r.events <- ev:
		return nil
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

func (r *receiver) run() error {
	s := r.s
	c := s.c
	phase := 0
	maxPhase := 1
	if c.protocol >= 29 {
		maxPhase = 2
	}
	tok := newTokenReceiver(c, s.compression)
	for {
		req, err := s.readNdxAndAttrs(len(r.files))
		if err != nil {
			return err
		}
		if req.ndx == ndxDone {
			phase++
			if phase > maxPhase {
				break
			}
			if err := r.notify(recvEvent{done: true}); err != nil {
				return err
			}
			continue
		}
		f := r.files[req.ndx]
		if f.inactive {
			return s.fatal(exitProtocol, fmt.Errorf("refusing transfer of cleared file index %d", req.ndx))
		}
		if req.iflags&itemTransfer == 0 {
			continue
		}
		if !f.isReg() {
			return s.fatal(exitProtocol, fmt.Errorf("received request to transfer non-regular file: %d", req.ndx))
		}
		if phase == 2 {
			return s.fatal(exitProtocol, errors.New("got transfer request in phase 2"))
		}
		redoing := r.sent[req.ndx]
		if s.opts.dryRun {
			continue
		}

		err = r.receiveFile(f, tok)
		switch {
		case err == nil:
			if s.opts.removeSource {
				if err := c.writeMsgInt(msgSuccess, req.ndx); err != nil {
					return err
				}
			}
		case errors.Is(err, errChecksumMismatch):
			if redoing {
				s.xferError("ERROR: %s failed verification -- update discarded.", f.name())
				continue
			}
			if s.opts.infoName > 0 {
				s.warning("WARNING: %s failed verification -- update discarded (will try again).", f.name())
			}
			r.sent[req.ndx] = true
			if err := r.notify(recvEvent{redo: req.ndx}); err != nil {
				return err
			}
		default:
			var putErr *putError
			if !errors.As(err, &putErr) {
				return err
			}
			// Storage refusing a file (pgs rejecting a dotfile, say) is
			// reported without failing the whole run.
			s.warning("rsync: [receiver] %s not stored: %v", f.name(), putErr.err)
		}
	}
	if err := r.notify(recvEvent{done: true}); err != nil {
		return err
	}

	if c.protocol >= 31 {
		// The sender confirms our final NDX_DONE with one of its own.
		req, err := s.readNdxAndAttrs(0)
		if err != nil {
			return err
		}
		if req.ndx != ndxDone {
			return s.fatal(exitProtocol, fmt.Errorf("invalid packet at end of run (%d)", req.ndx))
		}
		return r.notify(recvEvent{done: true})
	}
	return nil
}

// putError is a failure of the storage backend to keep a file.
type putError struct{ err error }

func (e *putError) Error() string { return e.err.Error() }

// receiveFile reads one file's delta, rebuilds the file from it and our old
// copy, and streams the result to storage. The stream only ends cleanly
// once the whole-file checksum matched (receiver.c:receive_data).
func (r *receiver) receiveFile(f *fileEntry, tok tokenReceiver) error {
	s := r.s
	target := r.dest.path(f)

	head, err := s.c.readSumHead(s.cs.xfer.size())
	if err != nil {
		return s.fatal(exitProtocol, err)
	}

	var basis File
	var basisSize int64
	if head.count > 0 {
		b, info, err := s.fs.Open(target)
		if err == nil {
			basis, basisSize = b, info.Size
			defer func() { _ = basis.Close() }()
		}
	}

	pr, pw := io.Pipe()
	type result struct {
		msg string
		err error
	}
	resc := make(chan result, 1)
	go func() {
		info := FileInfo{Name: target, Size: f.size, ModTime: time.Unix(f.mtime, int64(f.nsec))}
		msg, err := s.fs.Put(target, info, pr)
		if err == nil {
			err = errPutReturned
		}
		_ = pr.CloseWithError(err)
		if errors.Is(err, errPutReturned) {
			err = nil
		}
		resc <- result{msg, err}
	}()

	sum := s.cs.newXferSum()
	write := func(p []byte) {
		sum.Write(p)
		// After a failed Put the pipe returns its error; keep reading the
		// protocol stream regardless.
		_, _ = pw.Write(p)
	}

	var block []byte
	var tokErr error
	for {
		i, data, err := tok.recv()
		if err != nil {
			tokErr = err
			break
		}
		if i == 0 {
			break
		}
		if i > 0 {
			write(data)
			continue
		}
		idx := -(i + 1)
		if idx >= head.count {
			tokErr = s.fatal(exitProtocol, fmt.Errorf("invalid block index %d (count=%d)", idx, head.count))
			break
		}
		l := head.blength
		if idx == head.count-1 && head.remainder != 0 {
			l = head.remainder
		}
		offset := int64(idx) * int64(head.blength)
		if basis == nil || offset+int64(l) > basisSize {
			tokErr = s.fatal(exitProtocol, fmt.Errorf("got a block match with no basis file for %q", "/"+target))
			break
		}
		if cap(block) < int(l) {
			block = make([]byte, l)
		}
		block = block[:l]
		if err := readFullAt(basis, block, offset); err != nil {
			tokErr = s.fatal(exitFileIO, fmt.Errorf("failed to read basis %q: %w", "/"+target, err))
			break
		}
		tok.see(block)
		write(block)
	}
	if tokErr != nil {
		_ = pw.CloseWithError(tokErr)
		<-resc
		return tokErr
	}

	remote := make([]byte, s.cs.xfer.size())
	if err := s.c.readFull(remote); err != nil {
		_ = pw.CloseWithError(err)
		<-resc
		return err
	}
	if !bytes.Equal(sum.Sum(nil), remote) {
		_ = pw.CloseWithError(errChecksumMismatch)
		<-resc
		return errChecksumMismatch
	}
	_ = pw.Close()
	res := <-resc
	if res.err != nil {
		return &putError{err: res.err}
	}
	if res.msg != "" {
		s.info("%s", res.msg)
	}
	return nil
}

// errPutReturned unblocks writers to the pipe once Put is done reading.
var errPutReturned = errors.New("rsync: storage finished reading")
