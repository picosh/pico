package rsync

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/picosh/pico/pkg/rsync/rsyncopts"
)

// Config configures a server session.
type Config struct {
	FS     FS
	Logger *slog.Logger
	// Stderr receives errors raised before the protocol is running.
	Stderr io.Writer
}

// ExitError carries the exit code the client should see. Its message has
// already been delivered to the client.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("rsync exited with code %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// options holds the parsed command line in the form the session uses.
type options struct {
	sender         bool
	recurse        bool
	xferDirs       bool
	dryRun         bool
	preserveUID    bool
	preserveGID    bool
	preserveLinks  bool
	preserveDevs   bool
	preserveSpecs  bool
	preserveTimes  bool
	preserveAtimes bool
	numericIDs     bool
	alwaysChecksum bool
	ignoreTimes    bool
	sizeOnly       bool
	updateOnly     bool
	ignoreExisting bool
	ignoreNonExist bool
	wholeFile      bool
	inplace        bool
	delayUpdates   bool
	removeSource   bool
	deleteMode     bool
	deleteBefore   bool
	deleteDuring   int
	deleteAfter    bool
	deleteExcluded bool
	pruneEmptyDirs bool
	ignoreErrors   bool
	cvsExclude     bool
	maxDelete      int
	maxSize        int64
	minSize        int64
	blockSize      int32
	infoName       int
	infoDel        int
	infoNonreg     int
}

type stats struct {
	literal int64
	matched int64
}

// session is one rsync server run.
type session struct {
	fs     FS
	logger *slog.Logger
	c      *conn
	opts   options

	cs             checksums
	compression    compression
	compressLevel  int
	filters        *filterList
	varintFlags    bool
	safeFlist      bool
	xmitID0Names   bool
	inplacePartial bool

	mu           sync.Mutex
	ioError      int32
	gotXferError bool
	stats        stats
}

// Serve runs the server side of an rsync transfer over rw, which carries the
// SSH session's stdin and stdout. args is the command line after "rsync".
func Serve(cfg Config, rw io.ReadWriter, args []string) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	early := func(code int, err error) error {
		_, _ = fmt.Fprintf(stderr, "rsync: %v\n", err)
		return &ExitError{Code: code, Err: err}
	}

	c := newConn(rw)
	defer func() { _ = c.close() }()
	pc, err := rsyncopts.ParseArguments(args)
	if err != nil {
		return early(exitSyntax, err)
	}
	if pc.Options.ProtectArgs() {
		// The real arguments follow on stdin, NUL-separated.
		extra, err := readProtectedArgs(c)
		if err != nil {
			return early(exitProtocol, err)
		}
		if pc, err = rsyncopts.ParseArguments(append(slices.Clone(args), extra...)); err != nil {
			return early(exitSyntax, err)
		}
	}
	o := pc.Options
	if !o.Server() {
		return early(exitSyntax, errors.New("only server mode is supported"))
	}
	if err := checkSupported(o); err != nil {
		return early(exitUnsupported, err)
	}

	s := &session{
		fs:      cfg.FS,
		logger:  logger,
		c:       c,
		filters: &filterList{},
	}
	s.opts = options{
		sender:         o.Sender(),
		recurse:        o.Recurse(),
		xferDirs:       o.XferDirs(),
		dryRun:         o.DryRun(),
		preserveUID:    o.PreserveUid(),
		preserveGID:    o.PreserveGid(),
		preserveLinks:  o.PreserveLinks(),
		preserveDevs:   o.PreserveDevices(),
		preserveSpecs:  o.PreserveSpecials(),
		preserveTimes:  o.PreserveMTimes(),
		preserveAtimes: o.PreserveAtimes(),
		numericIDs:     o.NumericIDs(),
		alwaysChecksum: o.AlwaysChecksum(),
		ignoreTimes:    o.IgnoreTimes(),
		sizeOnly:       o.SizeOnly(),
		updateOnly:     o.UpdateOnly(),
		ignoreExisting: o.IgnoreExisting(),
		ignoreNonExist: o.IgnoreNonExisting(),
		wholeFile:      o.WholeFile(),
		inplace:        o.Inplace(),
		delayUpdates:   o.DelayUpdates(),
		removeSource:   o.RemoveSourceFiles() != 0,
		deleteMode:     o.DeleteMode(),
		deleteBefore:   o.DeleteBefore(),
		deleteDuring:   o.DeleteDuring(),
		deleteAfter:    o.DeleteAfter(),
		deleteExcluded: o.DeleteExcluded(),
		pruneEmptyDirs: o.PruneEmptyDirs(),
		ignoreErrors:   o.IgnoreErrors(),
		cvsExclude:     o.CvsExclude(),
		maxDelete:      o.MaxDelete(),
		maxSize:        o.MaxSize(),
		minSize:        o.MinSize(),
		blockSize:      int32(o.BlockSize()),
		infoName:       o.Info(rsyncopts.INFO_NAME),
		infoDel:        o.Info(rsyncopts.INFO_DEL),
		infoNonreg:     o.Info(rsyncopts.INFO_NONREG),
	}

	if err := s.setupProtocol(o); err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			return early(exit.Code, exit.Err)
		}
		return early(exitProtocol, err)
	}

	if err := c.w.setMultiplex(true); err != nil {
		return &ExitError{Code: exitStreamIO, Err: err}
	}
	if s.c.protocol >= 30 {
		c.r.multiplex = true
	}
	c.r.onMsg = s.handleMsg

	logger.Debug("rsync session starting",
		"protocol", c.protocol,
		"sender", s.opts.sender,
		"checksum", s.cs.xfer,
		"compression", s.compression,
	)

	if s.opts.sender {
		err = s.runSender(pc.RemainingArgs)
	} else {
		err = s.runReceiver(pc.RemainingArgs)
	}
	if err == nil {
		return nil
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit
	}
	// An unexpected failure, most likely the connection going away.
	return s.fatal(exitStreamIO, err)
}

// checkSupported refuses options that would change the wire format in ways
// we don't implement, or that make no sense for a storage backend.
func checkSupported(o *rsyncopts.Options) error {
	switch {
	case o.PreserveHardLinks():
		return errors.New("--hard-links is not supported")
	case o.PreserveACLs():
		return errors.New("--acls is not supported")
	case o.PreserveXattrs():
		return errors.New("--xattrs is not supported")
	case o.PreserveCrtimes():
		return errors.New("--crtimes is not supported")
	case o.RelativePaths():
		return errors.New("--relative is not supported")
	case o.AppendMode() != 0:
		return errors.New("--append is not supported")
	case len(o.BasisDirs()) > 0:
		return errors.New("--link-dest, --copy-dest and --compare-dest are not supported")
	case o.BatchMode():
		return errors.New("batch mode is not supported")
	case o.FilesFrom() != "":
		return errors.New("--files-from is not supported")
	case o.Iconv() != "":
		return errors.New("--iconv is not supported")
	case o.MissingArgs() == 2:
		return errors.New("--delete-missing-args is not supported")
	case o.Sender() && o.RemoveSourceFiles() != 0:
		return errors.New("--remove-source-files is not supported when downloading")
	}
	return nil
}

// readProtectedArgs reads the arguments a --secluded-args client sends
// before the protocol starts. The first one is the program name.
func readProtectedArgs(c *conn) ([]string, error) {
	var args []string
	var cur []byte
	for {
		b, err := c.readByte()
		if err != nil {
			return nil, err
		}
		if b != 0 {
			cur = append(cur, b)
			continue
		}
		if len(cur) == 0 {
			break
		}
		args = append(args, string(cur))
		cur = cur[:0]
	}
	if len(args) > 0 {
		args = args[1:]
	}
	return args, nil
}

// setupProtocol runs the handshake that precedes the transfer
// (compat.c:setup_protocol).
func (s *session) setupProtocol(o *rsyncopts.Options) error {
	c := s.c
	version := maxProtocol
	if v := o.ProtocolVersion(); v > 0 && v < version {
		version = v
	}
	if err := c.writeInt32(int32(version)); err != nil {
		return err
	}
	if err := c.flush(); err != nil {
		return err
	}
	remote, err := c.readInt32()
	if err != nil {
		return err
	}
	if int(remote) < version {
		version = int(remote)
	}
	if version < minProtocol {
		return &ExitError{Code: exitProtocol, Err: fmt.Errorf("protocol version %d is too old, %d or newer is required", remote, minProtocol)}
	}
	c.protocol = version

	if s.opts.deleteMode && !s.opts.deleteBefore && s.opts.deleteDuring == 0 && !s.opts.deleteAfter {
		if version < 30 {
			s.opts.deleteBefore = true
		} else {
			s.opts.deleteDuring = 1
		}
	}

	negotiate := false
	if version >= 30 {
		info := clientInfo(o.ShellCmd())
		flags := 0
		if strings.Contains(info, "f") {
			flags |= cfSafeFlist
		}
		if strings.Contains(info, "x") {
			flags |= cfAvoidXattrOptim
		}
		if strings.Contains(info, "C") {
			flags |= cfChksumSeedFix
		}
		if strings.Contains(info, "I") {
			flags |= cfInplacePartialDir
		}
		if strings.Contains(info, "u") {
			flags |= cfID0Names
		}
		if strings.Contains(info, "v") {
			negotiate = true
			flags |= cfVarintFlistFlags
		}
		if strings.Contains(info, "V") {
			flags |= cfVarintFlistFlags
			err = c.writeByte(byte(flags))
		} else {
			err = c.writeVarint(int32(flags))
		}
		if err != nil {
			return err
		}
		s.cs.properSeedOrder = flags&cfChksumSeedFix != 0
		s.varintFlags = flags&cfVarintFlistFlags != 0
		s.xmitID0Names = flags&cfID0Names != 0
		s.safeFlist = flags&cfSafeFlist != 0 || version >= 31
		s.inplacePartial = flags&cfInplacePartialDir != 0
	}

	checksumChoice := o.ChecksumChoice()
	compressChoice := o.CompressChoice()
	wantCompress := o.Compress() || compressChoice != ""
	if negotiate {
		if checksumChoice == "" {
			if err := c.writeVstring(nameList(checksumNames)); err != nil {
				return err
			}
		}
		if wantCompress && compressChoice == "" {
			if err := c.writeVstring(nameList(compressNames)); err != nil {
				return err
			}
		}
		if err := c.flush(); err != nil {
			return err
		}
	}

	// Pick the transfer checksum.
	switch {
	case checksumChoice != "":
		xfer, file, _ := strings.Cut(checksumChoice, ",")
		if file == "" {
			file = xfer
		}
		if s.cs.xfer, err = checksumByChoice(xfer, version); err != nil {
			return &ExitError{Code: exitUnsupported, Err: err}
		}
		if s.cs.file, err = checksumByChoice(file, version); err != nil {
			return &ExitError{Code: exitUnsupported, Err: err}
		}
	case negotiate:
		list, err := c.readVstring(256)
		if err != nil {
			return err
		}
		name, ok := negotiateName(checksumNames, list)
		if !ok {
			return &ExitError{Code: exitUnsupported, Err: fmt.Errorf("failed to negotiate a checksum choice, client list: %s", list)}
		}
		s.cs.xfer, _ = parseChecksumName(name)
		s.cs.file = s.cs.xfer
	default:
		s.cs.xfer, _ = checksumByChoice("auto", version)
		s.cs.file = s.cs.xfer
	}

	// Pick the compression.
	s.compression = compressNone
	switch {
	case compressChoice != "":
		if s.compression, err = parseCompressName(compressChoice); err != nil {
			return &ExitError{Code: exitUnsupported, Err: err}
		}
	case wantCompress && negotiate:
		list, err := c.readVstring(256)
		if err != nil {
			return err
		}
		name, ok := negotiateName(compressNames, list)
		if !ok {
			return &ExitError{Code: exitUnsupported, Err: fmt.Errorf("failed to negotiate a compress choice, client list: %s", list)}
		}
		s.compression, _ = parseCompressName(name)
	case wantCompress:
		s.compression = compressZlib
	}
	s.compressLevel = 6
	if lvl := o.CompressLevel(); s.compression != compressNone && lvl >= 0 {
		if lvl == 0 {
			s.compression = compressNone
		} else {
			s.compressLevel = min(lvl, 9)
		}
	}

	seed := int32(o.ChecksumSeed())
	if seed == 0 {
		var b [4]byte
		_, _ = rand.Read(b[:])
		seed = int32(binary.LittleEndian.Uint32(b[:]) & 0x7FFFFFFF)
		if seed == 0 {
			seed = 1
		}
	}
	s.cs.seed = seed
	if err := c.writeInt32(seed); err != nil {
		return err
	}
	if s.cs.xfer == csumNone {
		s.opts.wholeFile = true
	}
	return c.flush()
}

// clientInfo extracts the capability letters the client appends to its -e
// option, as in "-e.LsfxCIvu".
func clientInfo(shellCmd string) string {
	if i := strings.IndexByte(shellCmd, '.'); i >= 0 {
		return shellCmd[i+1:]
	}
	return shellCmd
}

func checksumByChoice(name string, protocol int) (csumType, error) {
	if name == "auto" {
		if protocol >= 30 {
			return csumMD5, nil
		}
		return csumMD4Old, nil
	}
	return parseChecksumName(name)
}

func nameList[T any](items []struct {
	name string
	typ  T
}) string {
	var names []string
	seen := map[any]bool{}
	for _, it := range items {
		if seen[it.typ] {
			continue
		}
		seen[it.typ] = true
		names = append(names, it.name)
	}
	return strings.Join(names, " ")
}

// negotiateName picks our most preferred name that the client also listed.
func negotiateName[T any](items []struct {
	name string
	typ  T
}, list string) (string, bool) {
	theirs := strings.Fields(list)
	for _, it := range items {
		if slices.Contains(theirs, it.name) {
			return it.name, true
		}
	}
	return "", false
}

// handleMsg processes an out-of-band message from the client.
func (s *session) handleMsg(tag byte, data []byte) error {
	switch tag {
	case msgIOError:
		if len(data) != 4 {
			return fmt.Errorf("invalid multi-message %d:%d", tag, len(data))
		}
		s.addIOError(int32(binary.LittleEndian.Uint32(data)) & ioErrMask)
	case msgNoop:
	case msgErrorExit:
		code := 0
		if len(data) == 4 {
			code = int(int32(binary.LittleEndian.Uint32(data)))
		}
		return &remoteExitError{code: code}
	case msgInfo, msgError, msgErrorXfer, msgWarning:
		s.logger.Info("rsync client message", "msg", strings.TrimSpace(string(data)))
	case msgNoSend:
		// The sender couldn't open a file we asked for and has reported
		// why; nothing will arrive for it.
		if len(data) != 4 {
			return fmt.Errorf("invalid multi-message %d:%d", tag, len(data))
		}
	case msgSuccess, msgDeleted:
	default:
		return fmt.Errorf("unexpected tag %d", tag)
	}
	return nil
}

func (s *session) addIOError(v int32) {
	s.mu.Lock()
	s.ioError |= v
	s.mu.Unlock()
}

func (s *session) who() string {
	if s.opts.sender {
		return "sender"
	}
	return "receiver"
}

func (s *session) send(tag byte, msg string) {
	if s.c.protocol < 30 {
		switch tag {
		case msgError:
			tag = msgErrorXfer
		case msgWarning:
			tag = msgInfo
		}
	}
	if err := s.c.writeMsg(tag, []byte(msg)); err != nil {
		s.logger.Debug("rsync message not delivered", "err", err)
	}
}

func (s *session) info(format string, args ...any) {
	s.send(msgInfo, fmt.Sprintf(format, args...)+"\n")
}

func (s *session) warning(format string, args ...any) {
	s.send(msgWarning, fmt.Sprintf(format, args...)+"\n")
}

// xferError reports a failure affecting one file; the run continues and
// ends with a partial-transfer exit code.
func (s *session) xferError(format string, args ...any) {
	s.mu.Lock()
	s.gotXferError = true
	s.mu.Unlock()
	s.send(msgErrorXfer, fmt.Sprintf("rsync: [%s] %s\n", s.who(), fmt.Sprintf(format, args...)))
}

// fatal reports err to the client and ends the run.
func (s *session) fatal(code int, err error) error {
	var remote *remoteExitError
	if errors.As(err, &remote) {
		return &ExitError{Code: remote.code, Err: err}
	}
	s.logger.Error("rsync session failed", "err", err)
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		s.send(msgError, fmt.Sprintf("rsync: [%s] %v\n", s.who(), err))
		if s.c.protocol >= 31 {
			_ = s.c.writeMsgInt(msgErrorExit, int32(code))
		}
		_ = s.c.flush()
	}
	return &ExitError{Code: code, Err: err}
}

// finish ends a run that completed, reporting a partial transfer if any
// file failed.
func (s *session) finish() error {
	if err := s.c.flush(); err != nil {
		return err
	}
	s.mu.Lock()
	ioError, xferErr := s.ioError, s.gotXferError
	s.mu.Unlock()
	code := 0
	switch {
	case ioError&ioErrGeneral != 0 || xferErr:
		code = exitPartial
	case ioError&ioErrVanished != 0:
		code = exitVanished
	case ioError&ioErrDelLimit != 0:
		code = exitDelLimit
	}
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code, Err: fmt.Errorf("some files were not transferred (code %d)", code)}
}

func (s *session) newFlistCodec() *flistCodec {
	return &flistCodec{
		protocol:      s.c.protocol,
		varintFlags:   s.varintFlags,
		safeFlist:     s.safeFlist,
		preserveUID:   s.opts.preserveUID,
		preserveGID:   s.opts.preserveGID,
		preserveLinks: s.opts.preserveLinks,
		preserveDevs:  s.opts.preserveDevs,
		preserveSpecs: s.opts.preserveSpecs,
		preserveAtime: s.opts.preserveAtimes,
		alwaysSum:     s.opts.alwaysChecksum,
		sumLen:        s.cs.file.size(),
	}
}

// recvFilterList reads the client's filter rules (exclude.c:recv_filter_list).
func (s *session) recvFilterList() error {
	receiverWants := s.opts.pruneEmptyDirs || (s.opts.deleteMode && (!s.opts.deleteExcluded || s.c.protocol >= 29))
	if s.opts.sender || receiverWants {
		for {
			n, err := s.c.readInt32()
			if err != nil {
				return err
			}
			if n == 0 {
				break
			}
			if n < 0 || n >= maxPathLen*2 {
				return s.fatal(exitProtocol, fmt.Errorf("filter rule too long: %d", n))
			}
			line := make([]byte, n)
			if err := s.c.readFull(line); err != nil {
				return err
			}
			if err := s.filters.parse(string(line), s.c.protocol < 29); err != nil {
				return s.fatal(exitSyntax, err)
			}
		}
	}
	if s.opts.cvsExclude && (s.opts.sender || s.c.protocol < 29) {
		for _, pat := range strings.Fields(defaultCVSIgnore) {
			s.filters.add(pat, ruleNoPrefixes)
		}
	}
	return nil
}

type ndxRequest struct {
	ndx          int32
	iflags       int
	fnamecmpType byte
	xname        string
	hasXname     bool
}

// readNdxAndAttrs reads the next file index and its item flags
// (rsync.c:read_ndx_and_attrs).
func (s *session) readNdxAndAttrs(count int) (ndxRequest, error) {
	c := s.c
	for {
		ndx, err := c.readNdx()
		if err != nil {
			return ndxRequest{}, err
		}
		if ndx == ndxDone {
			return ndxRequest{ndx: ndx}, nil
		}
		if ndx == ndxDelStats {
			var counts [5]int32
			for i := range counts {
				if counts[i], err = c.readVarint(); err != nil {
					return ndxRequest{}, err
				}
			}
			if s.opts.sender {
				// Echo the receiver's deletion stats back to the client.
				if err := s.writeDelStats(counts); err != nil {
					return ndxRequest{}, err
				}
			}
			continue
		}
		if ndx < 0 || int(ndx) > count {
			return ndxRequest{}, s.fatal(exitProtocol, fmt.Errorf("invalid file index: %d (%d - %d)", ndx, ndxDone, count-1))
		}
		req := ndxRequest{ndx: ndx, iflags: itemTransfer | itemMissingData, fnamecmpType: fnamecmpFname}
		if c.protocol >= 29 {
			v, err := c.readShortint()
			if err != nil {
				return ndxRequest{}, err
			}
			req.iflags = int(v)
		}
		// The protocol 29 keep-alive.
		if c.protocol < 30 && int(ndx) == count && req.iflags == itemIsNew {
			continue
		}
		if int(ndx) == count {
			return ndxRequest{}, s.fatal(exitProtocol, fmt.Errorf("invalid file index: %d", ndx))
		}
		if req.iflags&itemBasisTypeFollows != 0 {
			b, err := c.readByte()
			if err != nil {
				return ndxRequest{}, err
			}
			req.fnamecmpType = b
		}
		if req.iflags&itemXnameFollows != 0 {
			if req.xname, err = c.readVstring(maxPathLen); err != nil {
				return ndxRequest{}, err
			}
			req.hasXname = true
		}
		return req, nil
	}
}

func (s *session) writeNdxAndAttrs(req ndxRequest) error {
	if err := s.c.writeNdx(req.ndx); err != nil {
		return err
	}
	if s.c.protocol < 29 {
		return nil
	}
	if err := s.c.writeShortint(uint16(req.iflags)); err != nil {
		return err
	}
	if req.iflags&itemBasisTypeFollows != 0 {
		if err := s.c.writeByte(req.fnamecmpType); err != nil {
			return err
		}
	}
	if req.iflags&itemXnameFollows != 0 {
		return s.c.writeVstring(req.xname)
	}
	return nil
}

func (s *session) writeDelStats(counts [5]int32) error {
	if err := s.c.writeNdx(ndxDelStats); err != nil {
		return err
	}
	for _, v := range counts {
		if err := s.c.writeVarint(v); err != nil {
			return err
		}
	}
	return nil
}

// readFinalGoodbye waits for the client's end-of-run index
// (main.c:read_final_goodbye).
func (s *session) readFinalGoodbye(sender bool) error {
	c := s.c
	var ndx int32
	var err error
	if c.protocol < 29 {
		ndx, err = c.readInt32()
	} else {
		var req ndxRequest
		req, err = s.readNdxAndAttrs(0)
		ndx = req.ndx
		if err == nil && c.protocol >= 31 && ndx == ndxDone && sender {
			if err = c.writeNdx(ndxDone); err != nil {
				return err
			}
			req, err = s.readNdxAndAttrs(0)
			ndx = req.ndx
		}
	}
	if err != nil {
		return err
	}
	if ndx != ndxDone {
		return s.fatal(exitProtocol, fmt.Errorf("invalid packet at end of run (%d)", ndx))
	}
	return nil
}
