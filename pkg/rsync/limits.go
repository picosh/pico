package rsync

import "errors"

const (
	maxFileListEntries = 100_000
	maxFileListBytes   = 16 << 20
	maxFilterRules     = 4096
	maxFilterBytes     = 1 << 20
	maxArgs            = 1024
	maxArgBytes        = 1 << 20
	maxArgLength       = maxPathLen * 2
	maxMessageBytes    = maxPathLen * 2
)

var errFileListLimit = errors.New("file list exceeds metadata limit")
var errFilterListLimit = errors.New("filter list exceeds metadata limit")

// metadataBudget bounds retained data even when the wire format compresses it.
type metadataBudget struct {
	items, bytes int
	maxItems     int
	maxBytes     int
}

func (b *metadataBudget) add(size int) bool {
	if b.items >= b.maxItems || size < 0 || size > b.maxBytes-b.bytes {
		return false
	}
	b.items++
	b.bytes += size
	return true
}

func (b *metadataBudget) addFile(f *fileEntry) bool {
	return b.add(len(f.dirname) + len(f.basename) + len(f.link) + len(f.sum))
}

func validateListing(entries []FileInfo) error {
	budget := metadataBudget{maxItems: maxFileListEntries, maxBytes: maxFileListBytes}
	for _, e := range entries {
		if !budget.add(len(e.Name)) {
			return errFileListLimit
		}
	}
	return nil
}
