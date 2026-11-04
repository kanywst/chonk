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
		dev:   st.Dev,
		id:    fileID{st.Dev, st.Ino},
		nlink: uint64(st.Nlink), //nolint:unconvert // uint32 on some architectures
		alloc: st.Blocks * 512,
		mtime: st.Mtim.Sec,
	}
}
