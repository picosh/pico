package rsync

import (
	"bytes"
	"hash"
)

// matcher finds the receiver's blocks in the file being sent and emits the
// delta (rsync's match.c).
type matcher struct {
	head      sumHead
	sums      []sumBuf
	cs        *checksums
	tok       tokenSender
	buf       *mapFile
	sum       hash.Hash
	lastMatch int64
	inplace   bool // the receiver updates its basis file in place

	tablesize uint32
	table     []int32

	literal int64
	matched int64
}

// matched sends the literal data before offset and, for i >= 0, the token of
// block i. i == -1 ends the file and i == -2 only flushes literal data.
func (m *matcher) emit(offset int64, i int32) error {
	n := offset - m.lastMatch
	var block []byte
	if i >= 0 {
		block = m.buf.ptr(offset, int(m.sums[i].len))
		// Copy, the literal ptr below may move the window.
		block = append([]byte(nil), block...)
	}
	literal := m.buf.ptr(m.lastMatch, int(n))
	if err := m.tok.send(i, literal, block); err != nil {
		return err
	}
	m.literal += n
	total := n
	if i >= 0 {
		m.matched += int64(m.sums[i].len)
		total += int64(m.sums[i].len)
	}
	for j := int64(0); j < total; j += chunkSize {
		n1 := min(chunkSize, total-j)
		m.sum.Write(m.buf.ptr(m.lastMatch+j, int(n1)))
	}
	if i >= 0 {
		m.lastMatch = offset + int64(m.sums[i].len)
	} else {
		m.lastMatch = offset
	}
	return nil
}

func (m *matcher) buildTable() {
	m.tablesize = uint32(m.head.count/8)*10 + 11
	if m.tablesize < traditionalTableSize {
		m.tablesize = traditionalTableSize
	}
	m.table = make([]int32, m.tablesize)
	for i := range m.table {
		m.table[i] = -1
	}
	for i := range m.sums {
		var t uint32
		if m.tablesize == traditionalTableSize {
			s := m.sums[i].sum1
			t = (s&0xFFFF + s>>16) & 0xFFFF
		} else {
			t = m.sums[i].sum1 % m.tablesize
		}
		m.sums[i].chain = m.table[t]
		m.table[t] = int32(i)
	}
}

// run sends a file of length size.
func (m *matcher) run(size int64) error {
	if size > 0 && m.head.count > 0 {
		m.buildTable()
		if err := m.hashSearch(size); err != nil {
			return err
		}
	} else {
		for j := m.lastMatch + chunkSize; j < size; j += chunkSize {
			if err := m.emit(j, -2); err != nil {
				return err
			}
		}
		if err := m.emit(size, -1); err != nil {
			return err
		}
	}
	return nil
}

func (m *matcher) hashSearch(size int64) error {
	var sum2 []byte
	wantI := int32(0)
	blength := int64(m.head.blength)
	k := min(size, blength)
	sum := checksum1(m.buf.ptr(0, int(k)))
	s1, s2 := sum&0xFFFF, sum>>16
	var offset, alignedOffset int64
	alignedI := int32(0)
	end := size + 1 - int64(m.sums[len(m.sums)-1].len)
	s2len := int(m.head.s2length)

	for {
		doneCsum2 := false
		var hashEntry uint32
		if m.tablesize == traditionalTableSize {
			hashEntry = (s1 + s2) & 0xFFFF
			sum = s1&0xffff | s2<<16
		} else {
			sum = s1&0xffff | s2<<16
			hashEntry = sum % m.tablesize
		}
		i := m.table[hashEntry]
		prev := &m.table[hashEntry]
		chainLen := 0
		for ; i >= 0; i = m.sums[i].chain {
			// An in-place receiver can only use blocks at or after the
			// current offset, or ones it has left untouched.
			if m.inplace && m.sums[i].offset < offset && !m.sums[i].same {
				*prev = m.sums[i].chain
				continue
			}
			prev = &m.sums[i].chain
			if sum != m.sums[i].sum1 {
				continue
			}
			chainLen++
			if chainLen > maxChainLen {
				break
			}
			l := int32(min(blength, size-offset))
			if l != m.sums[i].len {
				continue
			}
			if !doneCsum2 {
				sum2 = m.cs.blockSum(m.buf.ptr(offset, int(l)), sum2[:0])
				doneCsum2 = true
			}
			if !bytes.Equal(sum2[:s2len], m.sums[i].sum2) {
				continue
			}

			if m.inplace {
				for alignedOffset < offset {
					alignedOffset += blength
					alignedI++
				}
				if (offset == alignedOffset || (sum == 0 && int64(l) == blength && alignedOffset+int64(l) <= size)) && alignedI < m.head.count {
					ok := true
					if i != alignedI {
						a := &m.sums[alignedI]
						if sum != a.sum1 || l != a.len || !bytes.Equal(sum2[:s2len], a.sum2) {
							ok = false
						} else {
							i = alignedI
						}
					}
					if ok && offset != alignedOffset {
						backup := max(alignedOffset-m.lastMatch, 0)
						block := m.buf.ptr(alignedOffset-backup, int(int64(l)+backup))[backup:]
						if checksum1(block) != m.sums[i].sum1 ||
							!bytes.Equal(m.cs.blockSum(block, nil)[:s2len], m.sums[i].sum2) {
							ok = false
						} else {
							offset = alignedOffset
						}
					}
					if ok {
						m.sums[i].same = true
						wantI = i
					}
				}
			}

			if i != wantI && wantI < m.head.count &&
				(!m.inplace || m.sums[wantI].offset >= offset || m.sums[wantI].same) &&
				sum == m.sums[wantI].sum1 && l == m.sums[wantI].len &&
				bytes.Equal(sum2[:s2len], m.sums[wantI].sum2) {
				// Prefer the adjacent block so the token runs compress.
				i = wantI
			}
			wantI = i + 1

			if err := m.emit(offset, i); err != nil {
				return err
			}
			offset += int64(m.sums[i].len) - 1
			k = min(blength, size-offset)
			sum = checksum1(m.buf.ptr(offset, int(k)))
			s1, s2 = sum&0xFFFF, sum>>16
			break
		}

		backup := max(offset-m.lastMatch, 0)
		more := offset+k < size
		n := k + backup
		if more {
			n++
		}
		window := m.buf.ptr(offset-backup, int(n))[backup:]
		first := uint32(int8(window[0]))
		s1 -= first
		s2 -= uint32(k) * first
		if more {
			s1 += uint32(int8(window[k]))
			s2 += s1
		} else {
			k--
		}

		if backup >= blength+chunkSize && end-offset > chunkSize {
			if err := m.emit(offset-blength, -2); err != nil {
				return err
			}
		}

		offset++
		if offset >= end {
			break
		}
	}
	return m.emit(size, -1)
}
