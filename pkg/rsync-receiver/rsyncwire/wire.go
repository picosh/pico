package rsyncwire

import (
	"bytes"
	"encoding/binary"
	"io"
)

const (
	MsgData  uint8 = 0
	MsgInfo  uint8 = 2
	MsgError uint8 = 1
)

const mplexBase = 7

type MultiplexWriter struct {
	Writer io.Writer
}

func (w *MultiplexWriter) Write(p []byte) (n int, err error) {
	return w.WriteMsg(MsgData, p)
}

func (w *MultiplexWriter) WriteMsg(tag uint8, p []byte) (n int, err error) {
	header := uint32(mplexBase+tag)<<24 | uint32(len(p))
	// log.Printf("len %d (hex %x)", len(p), uint32(len(p)))
	// log.Printf("header=%v (%x)", header, header)
	if err := binary.Write(w.Writer, binary.LittleEndian, header); err != nil {
		return 0, err
	}
	return w.Writer.Write(p)
}

type Buffer struct {
	// buf.Write() never fails, making for a convenient API.
	buf bytes.Buffer
}

func (b *Buffer) WriteByte(data byte) error {
	return binary.Write(&b.buf, binary.LittleEndian, data)
}

func (b *Buffer) WriteInt32(data int32) {
	_ = binary.Write(&b.buf, binary.LittleEndian, data)
}

func (b *Buffer) WriteInt64(data int64) {
	// send as a 32-bit integer if possible
	if data <= 0x7FFFFFFF && data >= 0 {
		b.WriteInt32(int32(data))
		return
	}
	// otherwise, send -1 followed by the 64-bit integer
	b.WriteInt32(-1)
	_ = binary.Write(&b.buf, binary.LittleEndian, data)
}

func (b *Buffer) WriteString(data string) {
	_, _ = io.WriteString(&b.buf, data)
}

func (b *Buffer) String() string {
	return b.buf.String()
}

type Conn struct {
	Writer io.Writer
	Reader io.Reader
}

func (c *Conn) WriteByte(data byte) error {
	return binary.Write(c.Writer, binary.LittleEndian, data)
}

func (c *Conn) WriteInt32(data int32) error {
	return binary.Write(c.Writer, binary.LittleEndian, data)
}

func (c *Conn) WriteInt64(data int64) error {
	// send as a 32-bit integer if possible
	if data <= 0x7FFFFFFF && data >= 0 {
		return c.WriteInt32(int32(data))
	}
	// otherwise, send -1 followed by the 64-bit integer
	if err := c.WriteInt32(-1); err != nil {
		return err
	}
	return binary.Write(c.Writer, binary.LittleEndian, data)
}

func (c *Conn) WriteString(data string) error {
	_, err := io.WriteString(c.Writer, data)
	return err
}

func (c *Conn) ReadByte() (byte, error) {
	var buf [1]byte
	if _, err := io.ReadFull(c.Reader, buf[:]); err != nil {
		return 0, err
	}
	return buf[0], nil
}

func (c *Conn) ReadInt32() (int32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(c.Reader, buf[:]); err != nil {
		return 0, err
	}
	return int32(binary.LittleEndian.Uint32(buf[:])), nil
}

func (c *Conn) ReadInt64() (int64, error) {
	{
		data, err := c.ReadInt32()
		if err != nil {
			return 0, err
		}
		if data != -1 {
			// The value was small enough to fit into a 32 bit int, so it was
			// transferred directly.
			return int64(data), nil
		}
		// Otherwise, -1 was transmitted, followed by the int64.
	}
	var data int64
	if err := binary.Read(c.Reader, binary.LittleEndian, &data); err != nil {
		return 0, err
	}
	return data, nil
}

type CountingReader struct {
	R         io.Reader
	BytesRead int64
}

func (r *CountingReader) Read(p []byte) (n int, err error) {
	n, err = r.R.Read(p)
	r.BytesRead += int64(n)
	return n, err
}

type CountingWriter struct {
	W            io.Writer
	BytesWritten int64
}

func (w *CountingWriter) Write(p []byte) (n int, err error) {
	n, err = w.W.Write(p)
	w.BytesWritten += int64(n)
	return n, err
}

func CounterPair(r io.Reader, w io.Writer) (*CountingReader, *CountingWriter) {
	crd := &CountingReader{R: r}
	cwr := &CountingWriter{W: w}
	return crd, cwr
}
