package walk

import (
	"io/fs"
	"syscall"
)

func statOf(fi fs.FileInfo) stat {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return stat{alloc: fi.Size(), mtime: fi.ModTime().Unix()}
	}
	return stat{
		dev:   uint64(st.Dev),                 //nolint:gosec // a device number is never negative
		id:    fileID{uint64(st.Dev), st.Ino}, //nolint:gosec // same
		nlink: uint64(st.Nlink),
		alloc: st.Blocks * 512,
		mtime: st.Mtimespec.Sec,
	}
}
