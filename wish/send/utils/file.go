package utils

import (
	"os"
	"time"
)

type VirtualFile struct {
	FName    string
	FIsDir   bool
	FSize    int64
	FModTime time.Time
	FSys     any
}

func (f *VirtualFile) Name() string { return f.FName }
func (f *VirtualFile) Size() int64  { return f.FSize }
func (f *VirtualFile) Mode() os.FileMode {
	if f.FIsDir {
		return os.FileMode(0755) | os.ModeDir
	}
	return os.FileMode(0644)
}
func (f *VirtualFile) ModTime() time.Time {
	if f.FModTime.IsZero() {
		return time.Now()
	}
	return f.FModTime
}
func (f *VirtualFile) IsDir() bool { return f.FIsDir }
func (f *VirtualFile) Sys() any    { return f.FSys }
