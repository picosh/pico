package rsyncsender

import (
	"encoding/binary"
	"io"
	"os"
	"sort"

	"github.com/mmcloughlin/md4"
	"github.com/picosh/pico/pkg/rsync-receiver/rsync"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncchecksum"
	"github.com/picosh/pico/pkg/rsync-receiver/rsynccommon"
	"github.com/picosh/pico/pkg/rsync-receiver/utils"
)

// rsync/sender.c:send_files().
func (st *Transfer) SendFiles(fileList *fileList) error {
	phase := 0
	for {
		// receive data about receiver’s copy of the file list contents (not
		// ordered)
		// see (*rsync.Receiver).Generator()
		fileIndex, err := st.Conn.ReadInt32()
		if err != nil {
			return err
		}
		if fileIndex == -1 {
			if phase == 0 {
				phase++
				// acknowledge phase change by sending -1
				if err := st.Conn.WriteInt32(-1); err != nil {
					return err
				}
				continue
			}
			break
		}

		if st.Opts.DryRun() {
			if err := st.Conn.WriteInt32(fileIndex); err != nil {
				return err
			}
			continue
		}

		head, err := st.receiveSums()
		if err != nil {
			return err
		}

		// The following quotes are citations from
		// https://www.samba.org/~tridge/phd_thesis.pdf, section 3.2.6 The
		// signature search algorithm (PDF page 64).

		// rsync/match.c:build_hash_table
		targets := make([]target, len(head.Sums))
		tagTable := make(map[uint16]int) // TODO: or int32 more specifically?
		{
			// “The first step in the algorithm is to sort the received
			// signatures by a 16 bit hash of the fast signature.”
			for idx, sum := range head.Sums {
				targets[idx] = target{
					index: int32(idx),
					tag:   rsyncchecksum.Tag(sum.Sum1),
				}
			}
			sort.Slice(targets, func(i, j int) bool {
				return targets[i].tag < targets[j].tag
			})

			// “A 16 bit index table is then formed which takes a 16 bit hash
			// value and gives an index into the sorted signature table which
			// points to the first entry in the table which has a matching
			// hash.”
			for idx := len(head.Sums) - 1; idx >= 0; idx-- {
				tagTable[targets[idx].tag] = idx
			}
		}

		st.lastMatch = 0
		if len(head.Sums) == 0 {
			// fast path: send the whole file
			err = st.sendFile(fileIndex, fileList.Files[fileIndex])
		} else {
			err = st.hashSearch(targets, tagTable, head, fileIndex, fileList.Files[fileIndex])
		}
		if err != nil {
			if _, ok := err.(*os.PathError); ok {
				// OpenFile() failed. Log the error (server side only) and
				// proceed. Only starting with protocol 30, an I/O error flag is
				// sent after the file transfer phase.
				if os.IsNotExist(err) {
					st.Logger.Debug("file has vanished", "file", fileList.Files[fileIndex])
				} else {
					st.Logger.Error("sendFiles", "err", err)
				}
				continue
			} else {
				return err
			}
		}
	}

	// phase done
	if err := st.Conn.WriteInt32(-1); err != nil {
		return err
	}

	return nil
}

// rsync/sender.c:receive_sums().
func (st *Transfer) receiveSums() (rsync.SumHead, error) {
	var head rsync.SumHead
	if err := head.ReadFrom(st.Conn); err != nil {
		return head, err
	}
	var offset int64
	head.Sums = make([]rsync.SumBuf, int(head.ChecksumCount))
	for i := int32(0); i < head.ChecksumCount; i++ {
		shortChecksum, err := st.Conn.ReadInt32()
		if err != nil {
			return head, err
		}
		sb := rsync.SumBuf{
			Index:  i,
			Offset: offset,
			Sum1:   uint32(shortChecksum),
		}
		if i == head.ChecksumCount-1 && head.RemainderLength != 0 {
			sb.Len = int64(head.RemainderLength)
		} else {
			sb.Len = int64(head.BlockLength)
		}
		offset += sb.Len
		n, err := io.ReadFull(st.Conn.Reader, sb.Sum2[:head.ChecksumLength])
		if err != nil {
			return head, err
		}
		_ = n
		// log.Printf("chunk[%d] len=%d offset=%.0f sum1=%08x, sum2=%x",
		// 	i, sb.len, float64(sb.offset), sb.sum1, sb.sum2[:n])
		head.Sums[i] = sb
	}
	return head, nil
}

func (st *Transfer) sendFile(fileIndex int32, fl utils.SenderFile) error {
	// rsync/rsync.h defines CHUNK_SIZE as 32 * 1024. openrsync (tridge)
	// uses 256K, but standard rsync rejects tokens larger than 32K.
	const chunkSize = 32 * 1024

	fi, r, err := st.Files.Read(&fl)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	if err := st.Conn.WriteInt32(fileIndex); err != nil {
		return err
	}

	sh := rsynccommon.SumSizesSqroot(fi.Size())
	// log.Printf("sh = %+v", sh)
	if err := sh.WriteTo(st.Conn); err != nil {
		return err
	}

	h := md4.New()
	_ = binary.Write(h, binary.LittleEndian, st.Seed) // hash.Hash.Write never fails

	buf := make([]byte, chunkSize)
	for {
		shouldBreak := false
		n, err := r.Read(buf)
		if err != nil {
			if err == io.EOF {
				shouldBreak = true
			} else {
				return err
			}
		}
		chunk := buf[:n]

		if len(chunk) == 0 {
			break
		}

		_, err = h.Write(chunk)
		if err != nil {
			return err
		}
		// chunk size (“rawtok” variable in openrsync)
		if err := st.Conn.WriteInt32(int32(len(chunk))); err != nil {
			return err
		}
		if _, err := st.Conn.Writer.Write(chunk); err != nil {
			return err
		}

		if shouldBreak {
			break
		}
	}
	// transfer finished:
	if err := st.Conn.WriteInt32(0); err != nil {
		return err
	}

	sum := h.Sum(nil)
	// log.Printf("sum: %x (len = %d)", sum, len(sum))
	if _, err := st.Conn.Writer.Write(sum); err != nil {
		return err
	}
	return nil
}
