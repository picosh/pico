package rsync

import (
	"bytes"
	stdflate "compress/flate"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/flate"
)

// compression identifies the negotiated token compression.
type compression int

const (
	compressNone compression = iota
	compressZlib
	compressZlibx
)

// compressNames is our preference order for negotiation. zlib adds matched
// blocks to the deflate history; zlibx does not.
var compressNames = []struct {
	name string
	typ  compression
}{
	{"zlibx", compressZlibx},
	{"zlib", compressZlib},
	{"none", compressNone},
}

func parseCompressName(name string) (compression, error) {
	for _, c := range compressNames {
		if c.name == name {
			return c.typ, nil
		}
	}
	return 0, fmt.Errorf("unknown compress name: %s", name)
}

// tokenSender emits one file's delta. token is a block index for a match, -1
// at the end of the file, or -2 to send literal data without a token.
// matched holds the matched block's data.
type tokenSender interface {
	send(token int32, literal, matched []byte) error
}

// tokenReceiver decodes one file's delta. recv returns a positive length
// with literal data, -(block+1) for a match, or 0 at the end of the file.
// see is called with each matched block's data.
type tokenReceiver interface {
	recv() (int32, []byte, error)
	see(data []byte)
}

func newTokenSender(c *conn, comp compression, level int) tokenSender {
	if comp == compressNone {
		return &simpleTokenSender{c: c}
	}
	return &deflateTokenSender{c: c, zlib: comp == compressZlib, level: level, lastToken: -1}
}

func newTokenReceiver(c *conn, comp compression) tokenReceiver {
	if comp == compressNone {
		return &simpleTokenReceiver{c: c, buf: make([]byte, chunkSize)}
	}
	return &deflateTokenReceiver{c: c, zlib: comp == compressZlib, out: make([]byte, chunkSize)}
}

type simpleTokenSender struct{ c *conn }

func (s *simpleTokenSender) send(token int32, literal, _ []byte) error {
	for len(literal) > 0 {
		n := min(len(literal), chunkSize)
		if err := s.c.writeInt32(int32(n)); err != nil {
			return err
		}
		if err := s.c.write(literal[:n]); err != nil {
			return err
		}
		literal = literal[n:]
	}
	if token == -2 {
		return nil
	}
	return s.c.writeInt32(-(token + 1))
}

type simpleTokenReceiver struct {
	c       *conn
	buf     []byte
	residue int32
}

func (s *simpleTokenReceiver) recv() (int32, []byte, error) {
	if s.residue == 0 {
		i, err := s.c.readInt32()
		if err != nil || i <= 0 {
			return i, nil, err
		}
		s.residue = i
	}
	n := min(int32(len(s.buf)), s.residue)
	s.residue -= n
	if err := s.c.readFull(s.buf[:n]); err != nil {
		return 0, nil, err
	}
	return n, s.buf[:n], nil
}

func (s *simpleTokenReceiver) see([]byte) {}

// Flag bytes of the compressed token stream.
const (
	tokEndFlag      = 0
	tokLong         = 0x20
	tokRunLong      = 0x21
	tokDeflatedData = 0x40
	tokRel          = 0x80
	tokRunRel       = 0xc0
	maxTokenIndex   = 0x7ffffffe
	deflateWindow   = 32 * 1024
)

var syncMarker = []byte{0, 0, 0xff, 0xff}

// deflateTokenSender produces rsync's compressed token stream: literal data
// is raw deflate, sync-flushed before every token with the trailing
// 00 00 ff ff marker stripped.
type deflateTokenSender struct {
	c     *conn
	zlib  bool
	level int

	w   *flate.Writer
	out bytes.Buffer

	lastToken, runStart, lastRunEnd int32
	flushPending                    bool

	history  []byte
	needDict bool
}

func (d *deflateTokenSender) send(token int32, literal, matched []byte) error {
	switch {
	case d.lastToken == -1:
		if err := d.resetStream(); err != nil {
			return err
		}
		d.lastRunEnd = 0
		d.runStart = token
		d.flushPending = false
	case d.lastToken == -2:
		d.runStart = token
	case len(literal) != 0 || token != d.lastToken+1 || token >= d.runStart+65536:
		if err := d.writeRun(); err != nil {
			return err
		}
		d.lastRunEnd = d.lastToken
		d.runStart = token
	}
	d.lastToken = token

	if len(literal) != 0 || d.flushPending {
		if err := d.deflate(literal, token != -2); err != nil {
			return err
		}
		d.flushPending = token == -2
	}

	if token == -1 {
		return d.c.writeByte(tokEndFlag)
	}
	if token != -2 && d.zlib {
		d.addHistory(matched, d.c.protocol)
		d.needDict = true
	}
	return nil
}

func (d *deflateTokenSender) resetStream() error {
	d.history = d.history[:0]
	d.needDict = false
	d.out.Reset()
	if d.w == nil {
		w, err := flate.NewWriter(&d.out, d.level)
		if err != nil {
			return err
		}
		d.w = w
		return nil
	}
	// Reset would reuse the last dictionary, so clear it explicitly.
	d.w.ResetDict(&d.out, nil)
	return nil
}

func (d *deflateTokenSender) writeRun() error {
	r := d.runStart - d.lastRunEnd
	n := d.lastToken - d.runStart
	if r >= 0 && r <= 63 {
		flag := byte(tokRel)
		if n != 0 {
			flag = tokRunRel
		}
		if err := d.c.writeByte(flag + byte(r)); err != nil {
			return err
		}
	} else {
		flag := byte(tokLong)
		if n != 0 {
			flag = tokRunLong
		}
		if err := d.c.writeByte(flag); err != nil {
			return err
		}
		if err := d.c.writeInt32(d.runStart); err != nil {
			return err
		}
	}
	if n != 0 {
		return d.c.write([]byte{byte(n), byte(n >> 8)})
	}
	return nil
}

func (d *deflateTokenSender) deflate(literal []byte, flush bool) error {
	if d.needDict {
		// The receiver's window holds the matched blocks too, so restart
		// the compressor with the same history.
		d.w.ResetDict(&d.out, d.window())
		d.needDict = false
	}
	if len(literal) > 0 {
		if _, err := d.w.Write(literal); err != nil {
			return err
		}
		d.addHistory(literal, 31)
	}
	if flush {
		if err := d.w.Flush(); err != nil {
			return err
		}
		if !bytes.HasSuffix(d.out.Bytes(), syncMarker) {
			return errors.New("rsync: deflate flush did not end in a sync marker")
		}
		d.out.Truncate(d.out.Len() - len(syncMarker))
	}
	data := d.out.Bytes()
	for len(data) > 0 {
		n := min(len(data), maxDataCount)
		if err := d.c.write([]byte{byte(tokDeflatedData + n>>8), byte(n)}); err != nil {
			return err
		}
		if err := d.c.write(data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	d.out.Reset()
	return nil
}

// addHistory appends data to the deflate history. Before protocol 31 rsync
// fed every 64K piece of a long matched block from its start, and both
// sides must reproduce that.
func (d *deflateTokenSender) addHistory(data []byte, protocol int) {
	d.history = appendHistory(d.history, data, protocol)
}

func (d *deflateTokenSender) window() []byte {
	if len(d.history) > deflateWindow {
		return d.history[len(d.history)-deflateWindow:]
	}
	return d.history
}

func appendHistory(history, data []byte, protocol int) []byte {
	toklen := len(data)
	src := data
	for toklen > 0 {
		n := min(toklen, 0xffff)
		history = append(history, src[:n]...)
		if protocol >= 31 {
			src = src[n:]
		}
		toklen -= n
	}
	if len(history) > 2*deflateWindow {
		history = append(history[:0], history[len(history)-deflateWindow:]...)
	}
	return history
}

type deflateRecvState int

const (
	rInit deflateRecvState = iota
	rIdle
	rRunning
	rInflating
)

// deflateTokenReceiver decodes the compressed token stream. Each run of
// DEFLATED_DATA chunks is inflated as one segment: the source appends the
// stripped sync marker when the next flag arrives and then reports EOF, and
// the inflater is restarted with the current history for the next segment.
type deflateTokenReceiver struct {
	c    *conn
	zlib bool

	state     deflateRecvState
	rxToken   int32
	rxRun     int32
	savedFlag int
	hasSaved  bool

	src     segmentSource
	inflate io.ReadCloser
	history []byte
	out     []byte
}

func (d *deflateTokenReceiver) recv() (int32, []byte, error) {
	for {
		switch d.state {
		case rInit:
			d.history = d.history[:0]
			d.rxToken = 0
			d.state = rIdle

		case rIdle:
			var flag int
			if d.hasSaved {
				flag, d.hasSaved = d.savedFlag, false
			} else {
				b, err := d.c.readByte()
				if err != nil {
					return 0, nil, err
				}
				flag = int(b)
			}
			if flag&0xC0 == tokDeflatedData {
				if err := d.src.start(d.c, flag); err != nil {
					return 0, nil, err
				}
				if d.inflate == nil {
					d.inflate = stdflate.NewReaderDict(&d.src, d.window())
				} else if err := d.inflate.(stdflate.Resetter).Reset(&d.src, d.window()); err != nil {
					return 0, nil, err
				}
				d.state = rInflating
				continue
			}
			if flag == tokEndFlag {
				d.state = rInit
				return 0, nil, nil
			}
			return d.tokenNum(flag)

		case rInflating:
			n, err := d.inflate.Read(d.out)
			if n > 0 {
				d.history = appendHistory(d.history, d.out[:n], 31)
				return int32(n), d.out[:n], nil
			}
			if err == nil {
				continue
			}
			if d.src.err != nil {
				return 0, nil, d.src.err
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) || !d.src.done() {
				return 0, nil, fmt.Errorf("rsync: inflate failed: %w", err)
			}
			d.savedFlag, d.hasSaved = d.src.next, true
			d.state = rIdle

		case rRunning:
			if d.rxRun <= 0 || d.rxToken >= maxTokenIndex {
				return 0, nil, errors.New("rsync: invalid token number in compressed stream")
			}
			d.rxToken++
			d.rxRun--
			if d.rxRun == 0 {
				d.state = rIdle
			}
			return -1 - d.rxToken, nil, nil
		}
	}
}

func (d *deflateTokenReceiver) tokenNum(flag int) (int32, []byte, error) {
	invalid := errors.New("rsync: invalid token number in compressed stream")
	if flag&tokRel != 0 {
		incr := int32(flag & 0x3f)
		if d.rxToken > maxTokenIndex-incr {
			return 0, nil, invalid
		}
		d.rxToken += incr
		flag >>= 6
	} else {
		v, err := d.c.readInt32()
		if err != nil {
			return 0, nil, err
		}
		if v < 0 || v > maxTokenIndex {
			return 0, nil, invalid
		}
		d.rxToken = v
	}
	if flag&1 != 0 {
		lo, err := d.c.readByte()
		if err != nil {
			return 0, nil, err
		}
		hi, err := d.c.readByte()
		if err != nil {
			return 0, nil, err
		}
		d.rxRun = int32(lo) + int32(hi)<<8
		if d.rxRun <= 0 || d.rxToken > maxTokenIndex-d.rxRun {
			return 0, nil, invalid
		}
		d.state = rRunning
	}
	return -1 - d.rxToken, nil, nil
}

func (d *deflateTokenReceiver) see(data []byte) {
	if d.zlib {
		d.history = appendHistory(d.history, data, d.c.protocol)
	}
}

func (d *deflateTokenReceiver) window() []byte {
	if len(d.history) > deflateWindow {
		return d.history[len(d.history)-deflateWindow:]
	}
	return d.history
}

// segmentSource feeds one segment of DEFLATED_DATA chunks to the inflater,
// reading further chunks from the connection on demand.
type segmentSource struct {
	c       *conn
	buf     []byte
	pos     int
	marker  int // bytes of the sync marker handed out, -1 before the segment ends
	next    int // flag byte that ended the segment
	err     error
	scratch [maxDataCount]byte
}

func (s *segmentSource) start(c *conn, flag int) error {
	s.c = c
	s.marker = -1
	s.err = nil
	return s.readChunk(flag)
}

func (s *segmentSource) readChunk(flag int) error {
	lo, err := s.c.readByte()
	if err != nil {
		return err
	}
	n := (flag&0x3f)<<8 + int(lo)
	if err := s.c.readFull(s.scratch[:n]); err != nil {
		return err
	}
	s.buf = s.scratch[:n]
	s.pos = 0
	return nil
}

func (s *segmentSource) done() bool { return s.marker == len(syncMarker) }

func (s *segmentSource) ReadByte() (byte, error) {
	for {
		if s.marker >= 0 {
			if s.marker == len(syncMarker) {
				return 0, io.EOF
			}
			b := syncMarker[s.marker]
			s.marker++
			return b, nil
		}
		if s.pos < len(s.buf) {
			b := s.buf[s.pos]
			s.pos++
			return b, nil
		}
		b, err := s.c.readByte()
		if err != nil {
			s.err = err
			return 0, err
		}
		if int(b)&0xC0 == tokDeflatedData {
			if err := s.readChunk(int(b)); err != nil {
				s.err = err
				return 0, err
			}
			continue
		}
		s.next = int(b)
		s.marker = 0
	}
}

func (s *segmentSource) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if s.marker < 0 && s.pos < len(s.buf) {
			m := copy(p[n:], s.buf[s.pos:])
			s.pos += m
			n += m
			continue
		}
		if n > 0 {
			return n, nil
		}
		b, err := s.ReadByte()
		if err != nil {
			return n, err
		}
		p[n] = b
		n++
	}
	return n, nil
}
