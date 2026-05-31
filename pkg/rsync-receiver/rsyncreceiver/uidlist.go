package rsyncreceiver

import (
	"io"
)

type mapping struct {
	Name    string
	LocalId int32
}

func (rt *Transfer) recvIdMapping1(localId func(id int32, name string) int32) (map[int32]mapping, error) {
	idMapping := make(map[int32]mapping)
	for {
		id, err := rt.Conn.ReadInt32()
		if err != nil {
			return nil, err
		}
		if id == 0 {
			break
		}
		length, err := rt.Conn.ReadByte()
		if err != nil {
			return nil, err
		}
		name := make([]byte, length)
		if _, err := io.ReadFull(rt.Conn.Reader, name); err != nil {
			return nil, err
		}
		idMapping[id] = mapping{
			Name:    string(name),
			LocalId: localId(id, string(name)),
		}
	}
	return idMapping, nil
}

// rsync/uidlist.c:recv_id_list.
func (rt *Transfer) RecvIdList() (users map[int32]mapping, groups map[int32]mapping, _ error) {
	if rt.Opts.PreserveUid {
		var err error
		users, err = rt.recvIdMapping1(func(remoteUid int32, remoteUsername string) int32 {
			// TODO: look up local uid by username
			return remoteUid
		})
		if err != nil {
			return nil, nil, err
		}
		for remoteUid, mapping := range users {
			rt.Logger.Debug("remote uid maps to local uid", "remoteUid", remoteUid, "name", mapping.Name, "localUid", mapping.LocalId)
		}
	}

	if rt.Opts.PreserveGid {
		var err error
		groups, err = rt.recvIdMapping1(func(remoteGid int32, remoteGroupname string) int32 {
			// TODO: look up local gid by groupname
			return remoteGid
		})
		if err != nil {
			return nil, nil, err
		}
		for remoteGid, mapping := range groups {
			rt.Logger.Debug("remote gid maps to local gid", "remoteGid", remoteGid, "name", mapping.Name, "localGid", mapping.LocalId)
		}
	}

	return users, groups, nil
}
