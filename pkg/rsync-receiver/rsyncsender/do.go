package rsyncsender

import (
	"fmt"
	"sort"

	"github.com/picosh/pico/pkg/rsync-receiver/rsyncstats"
	"github.com/picosh/pico/pkg/rsync-receiver/rsyncwire"
)

// rsync/main.c:client_run am_sender.
func (st *Transfer) Do(crd *rsyncwire.CountingReader, cwr *rsyncwire.CountingWriter, paths []string, exclusionList *filterRuleList) (*rsyncstats.TransferStats, error) {
	if exclusionList == nil {
		exclusionList = &filterRuleList{}
	}

	// “Update exchange” as per
	// https://github.com/kristapsdz/openrsync/blob/master/rsync.5

	// send file list
	fileList, err := st.SendFileList(st.Opts, paths, exclusionList)
	if err != nil {
		return nil, err
	}

	st.Logger.Debug("file list sent")

	// Sort the file list. The client sorts, so we need to sort, too (in the
	// same way!), otherwise our indices do not match what the client will
	// request.
	sort.Slice(fileList.Files, func(i, j int) bool {
		return fileList.Files[i].WPath < fileList.Files[j].WPath
	})

	if err := st.SendFiles(fileList); err != nil {
		return nil, err
	}

	// send statistics:
	// total bytes read (from network connection)
	if err := st.Conn.WriteInt64(crd.BytesRead); err != nil {
		return nil, err
	}
	// total bytes written (to network connection)
	if err := st.Conn.WriteInt64(cwr.BytesWritten); err != nil {
		return nil, err
	}
	// total size of files
	if err := st.Conn.WriteInt64(fileList.TotalSize); err != nil {
		return nil, err
	}

	st.Logger.Debug("reading final int32")

	finish, err := st.Conn.ReadInt32()
	if err != nil {
		return nil, err
	}
	if finish != -1 {
		return nil, fmt.Errorf("protocol error: expected final -1, got %d", finish)
	}

	return &rsyncstats.TransferStats{
		Read:    crd.BytesRead,
		Written: cwr.BytesWritten,
		Size:    fileList.TotalSize,
	}, nil
}
