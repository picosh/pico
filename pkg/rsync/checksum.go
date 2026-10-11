package rsync

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"hash"

	"github.com/cespare/xxhash/v2"
	"github.com/mmcloughlin/md4"
)

// csumType identifies a strong checksum. md4Old is the implied checksum of
// protocols 27-29, which seeds the whole-file sum; md4 is the same digest
// when negotiated by name in protocol 30+.
type csumType int

const (
	csumNone csumType = iota
	csumMD4Old
	csumMD4
	csumMD5
	csumXXH64
)

// checksumNames is our preference order for negotiation. It matches rsync's
// table order so both sides converge on the same choice.
var checksumNames = []struct {
	name string
	typ  csumType
}{
	{"xxh64", csumXXH64},
	{"xxhash", csumXXH64},
	{"md5", csumMD5},
	{"md4", csumMD4},
	{"none", csumNone},
}

func parseChecksumName(name string) (csumType, error) {
	for _, c := range checksumNames {
		if c.name == name {
			return c.typ, nil
		}
	}
	return 0, fmt.Errorf("unknown checksum name: %s", name)
}

func (t csumType) size() int {
	switch t {
	case csumNone:
		return 1
	case csumXXH64:
		return 8
	default:
		return 16
	}
}

// checksum1 is rsync's rolling weak checksum. The bytes are treated as
// signed chars, as rsync's C implementation does.
func checksum1(buf []byte) uint32 {
	var s1, s2 uint32
	i := 0
	for ; i < len(buf)-4; i += 4 {
		b0 := uint32(int8(buf[i]))
		b1 := uint32(int8(buf[i+1]))
		b2 := uint32(int8(buf[i+2]))
		b3 := uint32(int8(buf[i+3]))
		s2 += 4*(s1+b0) + 3*b1 + 2*b2 + b3
		s1 += b0 + b1 + b2 + b3
	}
	for ; i < len(buf); i++ {
		s1 += uint32(int8(buf[i]))
		s2 += s1
	}
	return (s1 & 0xffff) + (s2 << 16)
}

// checksums holds the negotiated digests and the session seed.
type checksums struct {
	xfer            csumType
	file            csumType
	seed            int32
	properSeedOrder bool
}

func (c *checksums) seedBytes() []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(c.seed))
	return b[:]
}

// blockSum is rsync's get_checksum2: the strong checksum of one block.
func (c *checksums) blockSum(buf []byte, out []byte) []byte {
	switch c.xfer {
	case csumXXH64:
		d := xxhash.NewWithSeed(uint64(int64(c.seed)))
		_, _ = d.Write(buf)
		return binary.LittleEndian.AppendUint64(out, d.Sum64())
	case csumMD5:
		h := md5.New()
		if c.properSeedOrder {
			if c.seed != 0 {
				h.Write(c.seedBytes())
			}
			h.Write(buf)
		} else {
			h.Write(buf)
			if c.seed != 0 {
				h.Write(c.seedBytes())
			}
		}
		return h.Sum(out)
	case csumMD4, csumMD4Old:
		h := md4.New()
		h.Write(buf)
		if c.seed != 0 {
			h.Write(c.seedBytes())
		}
		return h.Sum(out)
	default:
		return append(out, 0)
	}
}

// newXferSum starts the whole-file checksum that follows each transferred
// file (rsync's sum_init with the transfer digest).
func (c *checksums) newXferSum() hash.Hash {
	switch c.xfer {
	case csumMD4Old:
		h := md4.New()
		h.Write(c.seedBytes())
		return h
	default:
		return newPlainHash(c.xfer)
	}
}

// newFileSum starts the --checksum digest of a whole file.
func (c *checksums) newFileSum() hash.Hash {
	return newPlainHash(c.file)
}

func newPlainHash(t csumType) hash.Hash {
	switch t {
	case csumMD4, csumMD4Old:
		return md4.New()
	case csumMD5:
		return md5.New()
	case csumXXH64:
		return &xxh64LE{d: xxhash.NewWithSeed(0)}
	default:
		return &noneHash{}
	}
}

// xxh64LE emits the digest little-endian, as rsync's SIVAL64 does.
type xxh64LE struct{ d *xxhash.Digest }

func (x *xxh64LE) Write(p []byte) (int, error) { return x.d.Write(p) }
func (x *xxh64LE) Sum(b []byte) []byte         { return binary.LittleEndian.AppendUint64(b, x.d.Sum64()) }
func (x *xxh64LE) Reset()                      { x.d.Reset() }
func (x *xxh64LE) Size() int                   { return 8 }
func (x *xxh64LE) BlockSize() int              { return 32 }

type noneHash struct{}

func (noneHash) Write(p []byte) (int, error) { return len(p), nil }
func (noneHash) Sum(b []byte) []byte         { return append(b, 0) }
func (noneHash) Reset()                      {}
func (noneHash) Size() int                   { return 1 }
func (noneHash) BlockSize() int              { return 1 }
