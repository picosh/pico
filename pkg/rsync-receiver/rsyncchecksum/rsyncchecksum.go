package rsyncchecksum

import (
	"encoding/binary"
	"io"
	"os"

	"github.com/mmcloughlin/md4"
)

func Tag2(s1, s2 uint16) uint16 {
	return (((s1) + (s2)) & 0xFFFF)
}

func Tag(sum uint32) uint16 {
	return Tag2(uint16(sum&0xFFFF), uint16(sum>>16))
}

// SignExtend mirrors how C converts from (signed char) to uint32, i.e. using
// sign extension. get_checksum1 treats the buffer as (signed char*) instead of
// (unsigned char*), which likely was not a conscious choice, but here we are.
//
// This function is exported for use in the rolling checksum in match.go.
func SignExtend(b byte) uint32 {
	val := uint32(b)
	return uint32(int32(val<<24) >> 24)
}

func Checksum1(buf []byte) uint32 {
	bufLen := len(buf)
	var s1, s2 uint32
	var i int

	if bufLen > 4 {
		for i = 0; i < (bufLen - 4); i += 4 {
			s2 += 4*(s1+SignExtend(buf[i])) +
				3*SignExtend(buf[i+1]) +
				2*SignExtend(buf[i+2]) +
				SignExtend(buf[i+3])
			s1 += SignExtend(buf[i+0]) +
				SignExtend(buf[i+1]) +
				SignExtend(buf[i+2]) +
				SignExtend(buf[i+3])
		}
	}
	for ; i < bufLen; i++ {
		s1 += SignExtend(buf[i])
		s2 += s1
	}
	return (s1 & 0xffff) + (s2 << 16)
}

func Checksum2(seed int32, buf []byte) []byte {
	h := md4.New()
	h.Write(buf)
	_ = binary.Write(h, binary.LittleEndian, seed) // hash.Hash.Write never fails
	return h.Sum(nil)
}

func FileChecksum(fn string) ([]byte, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := md4.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

const Size = md4.Size
