package rsyncsender

import "io/fs"

func uidFromFileInfo(fs.FileInfo) (int32, bool) {
	return 1000, false
}

func gidFromFileInfo(fs.FileInfo) (int32, bool) {
	return 1000, false
}

func rdevFromFileInfo(fs.FileInfo) (int32, bool) {
	return 1000, false
}
