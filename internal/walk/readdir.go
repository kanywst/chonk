package walk

import (
	"io/fs"
	"os"
	"path/filepath"
)

type entryKind uint8

const (
	kindFile entryKind = iota
	kindDir
	kindLink
	kindOther
)

// entry is one directory entry with what the walk needs from its inode.
type entry struct {
	name  string
	kind  entryKind
	dev   uint64
	ino   uint64
	nlink uint64
	size  int64 // length
	alloc int64 // allocated bytes
	mtime int64
}

// readDirPortable lists a directory with ReadDir and an lstat per entry.
func readDirPortable(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	des, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil && len(des) == 0 {
		return nil, err
	}
	out := make([]entry, 0, len(des))
	for _, d := range des {
		fi, err := os.Lstat(filepath.Join(path, d.Name()))
		if err != nil {
			continue
		}
		st := statOf(fi)
		e := entry{name: d.Name(), dev: st.dev, ino: st.id.ino, nlink: st.nlink, size: fi.Size(), alloc: st.alloc, mtime: st.mtime}
		switch {
		case fi.IsDir():
			e.kind = kindDir
		case fi.Mode()&fs.ModeSymlink != 0:
			e.kind = kindLink
		case fi.Mode().IsRegular():
			e.kind = kindFile
		default:
			e.kind = kindOther
		}
		out = append(out, e)
	}
	return out, nil
}
