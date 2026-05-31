package rsyncsender

// rsync/token.c:simple_send_token.
func (st *Transfer) simpleSendToken(ms *mapStruct, token int32, offset int64, n int64) error {
	if n > 0 {
		st.Logger.Debug("sending unmatched chunks", "offset", offset, "n", n)
		l := int64(0)
		for l < n {
			n1 := min(int64(chunkSize), n-l)

			chunk := ms.ptr(offset+l, int32(n1))

			if err := st.Conn.WriteInt32(int32(n1)); err != nil {
				return err
			}

			if _, err := st.Conn.Writer.Write(chunk); err != nil {
				return err
			}

			l += n1
		}
	}
	if token != -2 {
		return st.Conn.WriteInt32(-(token + 1))
	}
	return nil
}

// rsync/token.c:send_token.
func (st *Transfer) sendToken(ms *mapStruct, i int32, offset int64, n int64) error {
	// TODO(compression): send deflated token
	return st.simpleSendToken(ms, i, offset, n)
}
