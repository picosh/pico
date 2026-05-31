package rsyncsender

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash"
	"os"

	"github.com/mmcloughlin/md4"
	"github.com/picosh/pico/pkg/rsync-receiver/nofollow"
	"github.com/picosh/pico/pkg/rsync-receiver/rsync"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncchecksum"
	"github.com/picosh/pico/pkg/rsync-receiver/utils"
)

type target struct {
	index int32
	tag   uint16
}

// rsync/match.c:hash_search.
func (st *Transfer) hashSearch(targets []target, tagTable map[uint16]int, head rsync.SumHead, fileIndex int32, fl utils.SenderFile) error {
	st.Logger.Debug("hashSearch", "file", fl, "head", head)
	f, err := os.OpenFile(fl.Path, os.O_RDONLY|nofollow.Maybe, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	readSize := max(3*head.BlockLength, 256*1024)
	ms := mapFile(f, fi.Size(), readSize, head.BlockLength)

	if err := st.Conn.WriteInt32(fileIndex); err != nil {
		return err
	}

	if err := head.WriteTo(st.Conn); err != nil {
		return err
	}

	// sum_init()
	h := md4.New()
	_ = binary.Write(h, binary.LittleEndian, st.Seed) // hash.Hash.Write never fails

	// The following quotes are citations from
	// https://www.samba.org/~tridge/phd_thesis.pdf, section 3.2.6 The
	// signature search algorithm (PDF page 64).

	// “Once the sorted signature table and the index table have been formed the
	// signature search process can begin. For each byte offset in a_i the fast
	// signature is computed, along with the 16 bit hash of the fast
	// signature. The 16 bit hash is then used to lookup the signature index,
	// giving the index in the signature table of the first fast signature with
	// that hash.”

	var k int
	var sum uint32
	var s1, s2 uint32
	var offset int64
	end := fi.Size() + 1 - head.Sums[len(head.Sums)-1].Len
	st.Logger.Debug("last block", "len", head.Sums[len(head.Sums)-1].Len, "end", end)

	readChunk := func() error {
		k = int(head.BlockLength)
		if remaining := int(fi.Size() - offset); remaining < k {
			k = remaining
		}

		chunk := ms.ptr(offset, int32(k))
		sum = rsyncchecksum.Checksum1(chunk)
		s1 = uint32(sum & 0xFFFF)
		s2 = uint32(sum >> 16)
		return nil
	}
	if err := readChunk(); err != nil {
		return err
	}

	tagHits := 0
Outer:
	for {
		tag := rsyncchecksum.Tag2(uint16(s1), uint16(s2))
		var sum2 []byte
		doneCsum2 := false
		j, ok := tagTable[tag]
		if ok {
			// “A linear search is then performed through the signature table, stopping
			// when an entry is found with a 16 bit hash which doesn’t match. For each
			// entry the current 32 bit fast signature is compared to the entry in the
			// signature table, and if that matches then the full 128 bit strong
			// signature is computed at the current byte offset and compared to the
			// strong signature in the signature table”
			sum = (uint32(s1) & 0xFFFF) | (uint32(s2) << 16)
			tagHits++
			for ; j < int(head.ChecksumCount) && targets[j].tag == tag; j++ {
				i := targets[j].index
				if sum != head.Sums[i].Sum1 {
					continue
				}

				l := int64(head.BlockLength)
				if v := fi.Size() - offset; v < l {
					l = v
				}
				if l != head.Sums[i].Len {
					continue
				}

				// log.Printf("potential match at %d target=%d %d sum=%08x", offset, j, i, sum)

				if !doneCsum2 {
					buf := ms.ptr(offset, int32(l))
					sum2 = rsyncchecksum.Checksum2(st.Seed, buf[:])
					doneCsum2 = true
				}

				if local, remote := sum2[:head.ChecksumLength], head.Sums[i].Sum2[:head.ChecksumLength]; !bytes.Equal(local, remote) {
					st.Logger.Debug("false alarm", "local", local, "remote", remote)
					//falseAlarms++
					continue
				}

				// TODO(optimization): tridge rsync locates adjacent matches
				// here for better run-length encoding, but I’m not sure where
				// (if at all) we currently use run-length encoding:
				// https://github.com/WayneD/rsync/commit/923fa978088f4c044eec528d9472962d9c9d13c3

				// “If the strong signature is found to match then A emits a
				// token telling B that a match was found and which block in bi
				// was matched12. The search then continues at the byte after
				// the matching block.”

				if err := st.matched(h, ms, head, offset, i); err != nil {
					return err
				}

				// rsync doesn’t read the next chunk (offset+sums[i].len),
				// rsync starts reading one byte before the next chunk
				// (offset+sums[i].len-1), because the code path starting at
				// “null_tag” removes the chunk’s first byte and adds the
				// next byte after the chunk.
				offset += head.Sums[i].Len - 1
				if err := readChunk(); err != nil {
					return fmt.Errorf("readChunk: %v", err)
				}

				if offset >= end {
					break Outer
				}

				break
			}
		}

		// Update the rolling checksum by removing the oldest byte (update[0])
		// and adding the newest byte (update[k]).
		backup := max(offset-st.lastMatch, 0)

		more := offset+int64(k) < fi.Size()
		mmore := int64(0)
		if more {
			mmore = 1
		}
		update := ms.ptr(offset-backup, int32(int64(k)+mmore+backup))
		update = update[backup:]

		s1 -= rsyncchecksum.SignExtend(update[0])
		s2 -= uint32(k) * rsyncchecksum.SignExtend(update[0])

		if more {
			s1 += rsyncchecksum.SignExtend(update[k])
			s2 += s1
		} else {
			k--
		}
		s1 = uint32(uint16(s1))
		s2 = uint32(uint16(s2))

		if backup >= int64(head.BlockLength)+chunkSize && end-offset > chunkSize {
			// Prevent offset-st.lastMatch from growing too large by flushing
			// intermediate chunks.
			if err := st.matched(h, ms, head, offset-int64(head.BlockLength), -2); err != nil {
				return err
			}
		}

		offset++
		if offset >= end {
			break
		}
	}

	if err := st.matched(h, ms, head, fi.Size(), -1); err != nil {
		return err
	}

	{
		sum := h.Sum(nil)
		st.Logger.Debug("sum info", "sum", sum, "len", len(sum))
		if _, err := st.Conn.Writer.Write(sum); err != nil {
			return err
		}
	}

	return nil

}

// rsync/match.c:matched.
func (st *Transfer) matched(h hash.Hash, ms *mapStruct, head rsync.SumHead, offset int64, i int32) error {
	n := offset - st.lastMatch

	transmitAccumulated := i < 0

	// if !transmitAccumulated {
	// 	log.Printf("match at offset=%d last_match=%d i=%d len=%d n=%d",
	// 		offset, st.lastMatch, i, head.Sums[i].Len, n)
	// } else {
	// 	log.Printf("transmit accumulated at offset=%d", offset)
	// }

	/* FIXME: this is not used
	l := int64(0)
	if !transmitAccumulated {
		l = head.Sums[i].Len
	}
	*/

	if err := st.sendToken(ms, i, st.lastMatch, n); err != nil {
		return fmt.Errorf("sendToken: %v", err)
	}
	// TODO: data_transfer += n;

	if !transmitAccumulated {
		// stats.matched_data += s->sums[i].len;
		n += head.Sums[i].Len
	}

	for j := int64(0); j < n; j += chunkSize {
		n1 := min(int64(chunkSize), n-j)
		chunk := ms.ptr(st.lastMatch+j, int32(n1))
		h.Write(chunk)
	}

	if !transmitAccumulated {
		st.lastMatch = offset + head.Sums[i].Len
	} else {
		st.lastMatch = offset
	}
	return nil
}
