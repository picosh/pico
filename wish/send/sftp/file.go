package sftp

import (
	"os"
	"time"
)

type tempfile struct {
	name    string
	isDir   bool
	size    int64
	modTime time.Time
	sys     any
}

func (f *tempfile) Name() string { return f.name }
func (f *tempfile) Size() int64  { return f.size }
func (f *tempfile) Mode() os.FileMode {
	if f.isDir {
		return os.FileMode(0755) | os.ModeDir
	}
	return os.FileMode(0644)
}
func (f *tempfile) ModTime() time.Time {
	if f.modTime.IsZero() {
		return time.Now()
	}
	return f.modTime
}
func (f *tempfile) IsDir() bool { return f.isDir }
func (f *tempfile) Sys() any    { return f.sys }
