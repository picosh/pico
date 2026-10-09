package rsync

import (
	"encoding/binary"
	"fmt"
	"io"
)

// sumHead describes the block signatures of a basis file.
type sumHead struct {
	count     int32
	blength   int32
	s2length  int32
	remainder int32
}

type sumBuf struct {
	offset int64
	len    int32
	sum1   uint32
	sum2   []byte
	chain  int32
	same   bool // matched at the same offset during an in-place update
}

func (c *conn) writeSumHead(h sumHead) error {
	if err := c.writeInt32(h.count); err != nil {
		return err
	}
	if err := c.writeInt32(h.blength); err != nil {
		return err
	}
	if err := c.writeInt32(h.s2length); err != nil {
		return err
	}
	return c.writeInt32(h.remainder)
}

func (c *conn) readSumHead(xferLen int) (sumHead, error) {
	var h sumHead
	var err error
	maxBlength := int32(maxBlockSize)
	if c.protocol < 30 {
		maxBlength = oldMaxBlockSize
	}
	if h.count, err = c.readInt32(); err != nil {
		return h, err
	}
	if h.count < 0 {
		return h, fmt.Errorf("invalid checksum count %d", h.count)
	}
	if h.blength, err = c.readInt32(); err != nil {
		return h, err
	}
	if h.blength < 0 || h.blength > maxBlength {
		return h, fmt.Errorf("invalid block length %d", h.blength)
	}
	if h.count > 0 && h.blength == 0 {
		return h, fmt.Errorf("invalid zero block length")
	}
	if h.s2length, err = c.readInt32(); err != nil {
		return h, err
	}
	if h.s2length < 0 || int(h.s2length) > xferLen {
		return h, fmt.Errorf("invalid checksum length %d", h.s2length)
	}
	if h.remainder, err = c.readInt32(); err != nil {
		return h, err
	}
	if h.remainder < 0 || h.remainder > h.blength {
		return h, fmt.Errorf("invalid remainder length %d", h.remainder)
	}
	return h, nil
}

// sumSizesSqroot picks the block size (about the square root of the file
// length, rounded to a multiple of 8) and the strong checksum length.
func sumSizesSqroot(length int64, protocol int, fixedBlock int32, csumLength int, xferLen int) (sumHead, error) {
	maxS2 := min(sumLength, xferLen)
	var blength int32
	switch {
	case fixedBlock > 0:
		blength = fixedBlock
	case length <= blockSize*blockSize:
		blength = blockSize
	default:
		maxBlength := int32(maxBlockSize)
		if protocol < 30 {
			maxBlength = oldMaxBlockSize
		}
		c := int64(1)
		for l := length; ; {
			l >>= 2
			if l == 0 {
				break
			}
			c <<= 1
		}
		if c >= int64(maxBlength) {
			blength = maxBlength
		} else {
			var b int64
			for {
				b |= c
				if length < b*b {
					b &^= c
				}
				c >>= 1
				if c < 8 {
					break
				}
			}
			blength = int32(max(b, blockSize))
		}
	}

	var s2length int
	if csumLength == sumLength {
		s2length = maxS2
	} else {
		b := blocksumBias
		for l := length; ; {
			l >>= 1
			if l == 0 {
				break
			}
			b += 2
		}
		for c := blength; ; {
			c >>= 1
			if c == 0 || b == 0 {
				break
			}
			b--
		}
		s2length = (b + 1 - 32 + 7) / 8
		s2length = max(s2length, csumLength)
		s2length = min(s2length, maxS2)
	}

	remainder := int32(length % int64(blength))
	count := length / int64(blength)
	if remainder != 0 {
		count++
	}
	if count > 0x7FFFFFFF {
		return sumHead{}, fmt.Errorf("file is too large for checksum sending")
	}
	return sumHead{
		count:     int32(count),
		blength:   blength,
		s2length:  int32(s2length),
		remainder: remainder,
	}, nil
}

// encodeSums computes the block signatures of a basis file in their wire
// form. They are built up front so a read error can't leave a half-written
// signature on the stream.
func encodeSums(r io.ReaderAt, length int64, head sumHead, cs *checksums) ([]byte, error) {
	out := make([]byte, 0, int(head.count)*(4+int(head.s2length)))
	buf := make([]byte, head.blength)
	var sum2 []byte
	var offset int64
	for i := int32(0); i < head.count; i++ {
		n := min(int64(head.blength), length-offset)
		block := buf[:n]
		if err := readFullAt(r, block, offset); err != nil {
			return nil, err
		}
		offset += n
		out = binary.LittleEndian.AppendUint32(out, checksum1(block))
		sum2 = cs.blockSum(block, sum2[:0])
		out = append(out, sum2[:head.s2length]...)
	}
	return out, nil
}

// readFullAt fills p from r at off, treating a short read as an error.
func readFullAt(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}
