package rsync

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// remoteExitError is returned when the peer aborts with MSG_ERROR_EXIT.
type remoteExitError struct{ code int }

func (e *remoteExitError) Error() string {
	return fmt.Sprintf("remote side exited with code %d", e.code)
}

// muxWriter queues protocol data and messages in order and writes them from
// its own goroutine. Once multiplexing is on, data goes out as MSG_DATA
// frames. Writers of data block when too much is queued; messages never
// block, so the receiver can report progress while the generator waits on
// the peer.
type muxWriter struct {
	mu        sync.Mutex
	cond      *sync.Cond
	w         io.Writer
	queue     []muxItem
	queued    int
	kick      bool
	busy      bool
	multiplex bool
	written   int64
	err       error
	closed    bool
}

type muxItem struct {
	tag  byte
	data []byte
}

const (
	muxBatchSize = 32 * 1024
	muxMaxQueued = 256 * 1024
)

func newMuxWriter(w io.Writer) *muxWriter {
	m := &muxWriter{w: w}
	m.cond = sync.NewCond(&m.mu)
	go m.pump()
	return m
}

func (m *muxWriter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.err == nil && m.queued >= muxMaxQueued {
		m.kick = true
		m.cond.Broadcast()
		m.cond.Wait()
	}
	if m.err != nil {
		return 0, m.err
	}
	if n := len(m.queue); n > 0 && m.queue[n-1].tag == msgData && len(m.queue[n-1].data) < muxBatchSize {
		m.queue[n-1].data = append(m.queue[n-1].data, p...)
	} else {
		m.queue = append(m.queue, muxItem{tag: msgData, data: append([]byte(nil), p...)})
	}
	m.queued += len(p)
	if m.queued >= muxBatchSize {
		m.kick = true
		m.cond.Broadcast()
	}
	return len(p), nil
}

// WriteMsg queues an out-of-band message behind any pending data.
func (m *muxWriter) WriteMsg(tag byte, p []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if !m.multiplex {
		return errors.New("rsync: message sent before multiplexing started")
	}
	m.queue = append(m.queue, muxItem{tag: tag, data: append([]byte(nil), p...)})
	m.kick = true
	m.cond.Broadcast()
	return nil
}

// Kick asks for queued output to go out without waiting for it.
func (m *muxWriter) Kick() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queue) > 0 {
		m.kick = true
		m.cond.Broadcast()
	}
	return m.err
}

// Flush waits until everything queued has been written.
func (m *muxWriter) Flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.err == nil && (len(m.queue) > 0 || m.busy) {
		m.kick = true
		m.cond.Broadcast()
		m.cond.Wait()
	}
	return m.err
}

// Close flushes and stops the writer goroutine.
func (m *muxWriter) Close() error {
	err := m.Flush()
	m.mu.Lock()
	m.closed = true
	m.cond.Broadcast()
	m.mu.Unlock()
	return err
}

func (m *muxWriter) setMultiplex(on bool) error {
	if err := m.Flush(); err != nil {
		return err
	}
	m.mu.Lock()
	m.multiplex = on
	m.mu.Unlock()
	return nil
}

func (m *muxWriter) pump() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for {
		for !m.closed && m.err == nil && (!m.kick || len(m.queue) == 0) {
			m.cond.Wait()
		}
		if m.closed || m.err != nil {
			m.queue = nil
			m.cond.Broadcast()
			return
		}
		items := m.queue
		m.queue = nil
		m.queued = 0
		m.kick = false
		m.busy = true
		multiplex := m.multiplex
		m.cond.Broadcast()
		m.mu.Unlock()

		var n int64
		var err error
		for _, it := range items {
			var k int64
			k, err = writeItem(m.w, it, multiplex)
			n += k
			if err != nil {
				break
			}
		}

		m.mu.Lock()
		m.busy = false
		m.written += n
		if err != nil {
			m.err = err
		}
		m.cond.Broadcast()
	}
}

func writeItem(w io.Writer, it muxItem, multiplex bool) (int64, error) {
	if !multiplex {
		n, err := w.Write(it.data)
		return int64(n), err
	}
	var total int64
	data := it.data
	for {
		chunk := data[:min(len(data), 0xFFFFFF)]
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(mplexBase+int(it.tag))<<24|uint32(len(chunk)))
		n, err := w.Write(append(hdr[:], chunk...))
		total += int64(n)
		if err != nil {
			return total, err
		}
		data = data[len(chunk):]
		if len(data) == 0 {
			return total, nil
		}
	}
}

func (m *muxWriter) bytesWritten() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.written + int64(m.queued)
	for _, it := range m.queue {
		if it.tag != msgData {
			n += int64(len(it.data)) + 4
		}
	}
	return n
}

// flushingReader sends our pending output before it blocks on the peer,
// which is what keeps request/response exchanges from stalling.
type flushingReader struct {
	r     io.Reader
	flush func() error
	read  int64
}

func (f *flushingReader) Read(p []byte) (int, error) {
	if err := f.flush(); err != nil {
		return 0, err
	}
	n, err := f.r.Read(p)
	f.read += int64(n)
	return n, err
}

// muxReader returns the data stream of a possibly multiplexed input and
// hands every other message to onMsg.
type muxReader struct {
	r         *bufio.Reader
	src       *flushingReader
	multiplex bool
	remaining int
	onMsg     func(tag byte, data []byte) error
}

func (m *muxReader) fill() error {
	for m.remaining == 0 {
		var hdr [4]byte
		if _, err := io.ReadFull(m.r, hdr[:]); err != nil {
			return err
		}
		v := binary.LittleEndian.Uint32(hdr[:])
		tag := int(v>>24) - mplexBase
		size := int(v & 0xFFFFFF)
		if tag == msgData {
			m.remaining = size
			continue
		}
		if tag < 0 {
			return fmt.Errorf("rsync: unexpected tag %d", tag)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(m.r, data); err != nil {
			return err
		}
		if err := m.onMsg(byte(tag), data); err != nil {
			return err
		}
	}
	return nil
}

func (m *muxReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !m.multiplex {
		return m.r.Read(p)
	}
	if err := m.fill(); err != nil {
		return 0, err
	}
	n, err := m.r.Read(p[:min(len(p), m.remaining)])
	m.remaining -= n
	return n, err
}

func (m *muxReader) ReadByte() (byte, error) {
	if !m.multiplex {
		return m.r.ReadByte()
	}
	if err := m.fill(); err != nil {
		return 0, err
	}
	b, err := m.r.ReadByte()
	if err == nil {
		m.remaining--
	}
	return b, err
}

// ndxState holds the delta-encoding state of one direction of write_ndx.
type ndxState struct {
	prevPositive int32
	prevNegative int32
}

func newNdxState() ndxState { return ndxState{prevPositive: -1, prevNegative: 1} }

// conn carries the rsync protocol over the session's stdin and stdout.
type conn struct {
	protocol int
	r        *muxReader
	w        *muxWriter
	ndxIn    ndxState
	ndxOut   ndxState
	rbuf     [16]byte
	wbuf     [16]byte
}

func newConn(rw io.ReadWriter) *conn {
	w := newMuxWriter(rw)
	src := &flushingReader{r: rw, flush: w.Kick}
	c := &conn{
		r:      &muxReader{r: bufio.NewReaderSize(src, 64*1024), src: src},
		w:      w,
		ndxIn:  newNdxState(),
		ndxOut: newNdxState(),
	}
	c.r.onMsg = func(tag byte, data []byte) error {
		return fmt.Errorf("rsync: unexpected message %d", tag)
	}
	return c
}

func (c *conn) bytesRead() int64    { return c.r.src.read }
func (c *conn) bytesWritten() int64 { return c.w.bytesWritten() }
func (c *conn) flush() error        { return c.w.Flush() }
func (c *conn) close() error        { return c.w.Close() }

func (c *conn) readFull(p []byte) error {
	_, err := io.ReadFull(c.r, p)
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (c *conn) readByte() (byte, error) {
	b, err := c.r.ReadByte()
	if errors.Is(err, io.EOF) {
		return 0, io.ErrUnexpectedEOF
	}
	return b, err
}

func (c *conn) readInt32() (int32, error) {
	if err := c.readFull(c.rbuf[:4]); err != nil {
		return 0, err
	}
	return int32(binary.LittleEndian.Uint32(c.rbuf[:4])), nil
}

func (c *conn) readShortint() (uint16, error) {
	if err := c.readFull(c.rbuf[:2]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(c.rbuf[:2]), nil
}

// readLongint reads the pre-30 64-bit encoding: an int32, or -1 followed by
// the full 64-bit value.
func (c *conn) readLongint() (int64, error) {
	v, err := c.readInt32()
	if err != nil || v != -1 {
		return int64(v), err
	}
	if err := c.readFull(c.rbuf[:8]); err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(c.rbuf[:8])), nil
}

// intByteExtra maps the high bits of a varint's first byte to the number of
// bytes that follow it.
var intByteExtra = [64]int{
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	2, 2, 2, 2, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 5, 6,
}

func (c *conn) readVarint() (int32, error) {
	ch, err := c.readByte()
	if err != nil {
		return 0, err
	}
	var b [5]byte
	extra := intByteExtra[ch/4]
	if extra == 0 {
		return int32(ch), nil
	}
	if extra >= len(b) {
		return 0, errors.New("rsync: overflow in varint")
	}
	if err := c.readFull(b[:extra]); err != nil {
		return 0, err
	}
	bit := byte(1) << (8 - extra)
	b[extra] = ch & (bit - 1)
	return int32(binary.LittleEndian.Uint32(b[:4])), nil
}

func (c *conn) readVarlong(minBytes int) (int64, error) {
	var b2 [8]byte
	if err := c.readFull(b2[:minBytes]); err != nil {
		return 0, err
	}
	var b [9]byte
	copy(b[:], b2[1:minBytes])
	extra := intByteExtra[b2[0]/4]
	if extra == 0 {
		b[minBytes-1] = b2[0]
		return int64(binary.LittleEndian.Uint64(b[:8])), nil
	}
	if minBytes+extra > len(b) {
		return 0, errors.New("rsync: overflow in varlong")
	}
	if err := c.readFull(b[minBytes-1 : minBytes-1+extra]); err != nil {
		return 0, err
	}
	bit := byte(1) << (8 - extra)
	b[minBytes+extra-1] = b2[0] & (bit - 1)
	return int64(binary.LittleEndian.Uint64(b[:8])), nil
}

func (c *conn) readVarint30() (int32, error) {
	if c.protocol < 30 {
		return c.readInt32()
	}
	return c.readVarint()
}

func (c *conn) readVarlong30(minBytes int) (int64, error) {
	if c.protocol < 30 {
		return c.readLongint()
	}
	return c.readVarlong(minBytes)
}

func (c *conn) readVstring(max int) (string, error) {
	b, err := c.readByte()
	if err != nil {
		return "", err
	}
	n := int(b)
	if n&0x80 != 0 {
		lo, err := c.readByte()
		if err != nil {
			return "", err
		}
		n = (n&^0x80)*0x100 + int(lo)
	}
	if n >= max {
		return "", fmt.Errorf("rsync: over-long vstring received (%d > %d)", n, max-1)
	}
	buf := make([]byte, n)
	if err := c.readFull(buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// readNdx reads a file-list index, which protocol 30 delta-encodes.
func (c *conn) readNdx() (int32, error) {
	if c.protocol < 30 {
		return c.readInt32()
	}
	b0, err := c.readByte()
	if err != nil {
		return 0, err
	}
	prev := &c.ndxIn.prevPositive
	switch b0 {
	case 0:
		return ndxDone, nil
	case 0xFF:
		prev = &c.ndxIn.prevNegative
		if b0, err = c.readByte(); err != nil {
			return 0, err
		}
	}
	var unum uint32
	if b0 == 0xFE {
		var b [4]byte
		if err := c.readFull(b[:2]); err != nil {
			return 0, err
		}
		if b[0]&0x80 != 0 {
			b[3] = b[0] &^ 0x80
			b[0] = b[1]
			if err := c.readFull(b[1:3]); err != nil {
				return 0, err
			}
			unum = binary.LittleEndian.Uint32(b[:])
		} else {
			unum = uint32(b[0])<<8 + uint32(b[1]) + uint32(*prev)
		}
	} else {
		unum = uint32(b0) + uint32(*prev)
	}
	if unum > 0x7FFFFFFF {
		return 0, fmt.Errorf("rsync: invalid file index %d", unum)
	}
	num := int32(unum)
	*prev = num
	if prev == &c.ndxIn.prevNegative {
		num = -num
	}
	return num, nil
}

func (c *conn) write(p []byte) error {
	_, err := c.w.Write(p)
	return err
}

func (c *conn) writeByte(b byte) error {
	return c.write([]byte{b})
}

func (c *conn) writeInt32(v int32) error {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(v))
	return c.write(b[:])
}

func (c *conn) writeShortint(v uint16) error {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	return c.write(b[:])
}

func (c *conn) writeLongint(v int64) error {
	if v >= 0 && v <= 0x7FFFFFFF {
		return c.writeInt32(int32(v))
	}
	var b [12]byte
	binary.LittleEndian.PutUint32(b[:4], 0xFFFFFFFF)
	binary.LittleEndian.PutUint64(b[4:], uint64(v))
	return c.write(b[:])
}

func appendVarint(dst []byte, x int32) []byte {
	var b [5]byte
	binary.LittleEndian.PutUint32(b[1:], uint32(x))
	cnt := 4
	for cnt > 1 && b[cnt] == 0 {
		cnt--
	}
	bit := byte(1) << (7 - cnt + 1)
	switch {
	case b[cnt] >= bit:
		cnt++
		b[0] = ^(bit - 1)
	case cnt > 1:
		b[0] = b[cnt] | ^(bit*2 - 1)
	default:
		b[0] = b[1]
	}
	return append(dst, b[:cnt]...)
}

func appendVarlong(dst []byte, x int64, minBytes int) []byte {
	var b [9]byte
	binary.LittleEndian.PutUint64(b[1:], uint64(x))
	cnt := 8
	for cnt > minBytes && b[cnt] == 0 {
		cnt--
	}
	bit := byte(1) << (7 - cnt + minBytes)
	switch {
	case b[cnt] >= bit:
		cnt++
		b[0] = ^(bit - 1)
	case cnt > minBytes:
		b[0] = b[cnt] | ^(bit*2 - 1)
	default:
		b[0] = b[cnt]
	}
	return append(dst, b[:cnt]...)
}

func (c *conn) writeVarint(v int32) error {
	return c.write(appendVarint(c.wbuf[:0], v))
}

func (c *conn) writeVarlong(v int64, minBytes int) error {
	return c.write(appendVarlong(c.wbuf[:0], v, minBytes))
}

func (c *conn) writeVarint30(v int32) error {
	if c.protocol < 30 {
		return c.writeInt32(v)
	}
	return c.writeVarint(v)
}

func (c *conn) writeVarlong30(v int64, minBytes int) error {
	if c.protocol < 30 {
		return c.writeLongint(v)
	}
	return c.writeVarlong(v, minBytes)
}

func (c *conn) writeVstring(s string) error {
	n := len(s)
	if n > 0x7FFF {
		return fmt.Errorf("rsync: attempting to send over-long vstring (%d > %d)", n, 0x7FFF)
	}
	var hdr []byte
	if n > 0x7F {
		hdr = []byte{byte(n/0x100 + 0x80), byte(n)}
	} else {
		hdr = []byte{byte(n)}
	}
	if err := c.write(hdr); err != nil {
		return err
	}
	return c.write([]byte(s))
}

func (c *conn) writeNdx(ndx int32) error {
	if c.protocol < 30 {
		return c.writeInt32(ndx)
	}
	var b [6]byte
	cnt := 0
	var diff int32
	switch {
	case ndx >= 0:
		diff = ndx - c.ndxOut.prevPositive
		c.ndxOut.prevPositive = ndx
	case ndx == ndxDone:
		return c.writeByte(0)
	default:
		b[cnt] = 0xFF
		cnt++
		ndx = -ndx
		diff = ndx - c.ndxOut.prevNegative
		c.ndxOut.prevNegative = ndx
	}
	switch {
	case diff > 0 && diff < 0xFE:
		b[cnt] = byte(diff)
		cnt++
	case diff < 0 || diff > 0x7FFF:
		b[cnt] = 0xFE
		b[cnt+1] = byte(ndx>>24) | 0x80
		b[cnt+2] = byte(ndx)
		b[cnt+3] = byte(ndx >> 8)
		b[cnt+4] = byte(ndx >> 16)
		cnt += 5
	default:
		b[cnt] = 0xFE
		b[cnt+1] = byte(diff >> 8)
		b[cnt+2] = byte(diff)
		cnt += 3
	}
	return c.write(b[:cnt])
}

func (c *conn) writeMsg(tag byte, data []byte) error {
	return c.w.WriteMsg(tag, data)
}

func (c *conn) writeMsgInt(tag byte, v int32) error {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(v))
	return c.w.WriteMsg(tag, b[:])
}
