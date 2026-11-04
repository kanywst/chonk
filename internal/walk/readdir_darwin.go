package walk

import (
	"encoding/binary"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// readDir lists a directory with getattrlistbulk(2): one system call returns
// the names and sizes of many entries, where ReadDir plus an lstat per entry
// costs a call per file. It is several times faster on a large home.
func readDir(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	al := attrList{
		bitmapCount: unix.ATTR_BIT_MAP_COUNT,
		commonAttr:  unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_NAME | unix.ATTR_CMN_DEVID | unix.ATTR_CMN_OBJTYPE | unix.ATTR_CMN_MODTIME | unix.ATTR_CMN_FILEID,
		fileAttr:    unix.ATTR_FILE_LINKCOUNT | unix.ATTR_FILE_TOTALSIZE | unix.ATTR_FILE_ALLOCSIZE,
	}
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	buf := *bp
	var out []entry
	for {
		// x/sys has no libSystem wrapper for getattrlistbulk. Its number has
		// been 461 since macOS 10.10, and any error falls back to ReadDir.
		//nolint:gosec,staticcheck // G103: raw pointers to al and buf, live for the call; SA1019: see above
		n, _, errno := unix.Syscall6(unix.SYS_GETATTRLISTBULK, f.Fd(), uintptr(unsafe.Pointer(&al)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unix.FSOPT_PACK_INVAL_ATTRS), 0)
		if errno != 0 {
			if errno == unix.EINTR {
				continue
			}
			if out == nil {
				return readDirPortable(path)
			}
			return out, errno
		}
		if n == 0 {
			return out, nil
		}
		b := buf
		for range int(n) {
			if len(b) < 4 {
				break
			}
			size := int(binary.LittleEndian.Uint32(b))
			if size < 4 || size > len(b) {
				break
			}
			if e, ok := parseEntry(b[:size]); ok {
				out = append(out, e)
			}
			b = b[size:]
		}
	}
}

// bufPool reuses the bulk buffers: a home has hundreds of thousands of
// directories, and a fresh buffer for each one costs more than the calls.
var bufPool = sync.Pool{New: func() any { b := make([]byte, 64<<10); return &b }}

type attrList struct {
	bitmapCount uint16
	_           uint16
	commonAttr  uint32
	volAttr     uint32
	dirAttr     uint32
	fileAttr    uint32
	forkAttr    uint32
}

const (
	vreg = 1
	vdir = 2
	vlnk = 5
)

// parseEntry reads one record. With FSOPT_PACK_INVAL_ATTRS every requested
// common attribute is present, in bit order, each 4-byte aligned. File
// attributes are present only for non-directories, as the returned attribute
// set says:
//
//	u32 length | attribute_set_t (5 x u32) | name attrreference (i32 off, u32 len)
//	| devid i32 | objtype u32 | modtime timespec | fileid u64
//	[| linkcount u32 | totalsize i64 | allocsize i64]
func parseEntry(r []byte) (entry, bool) {
	const (
		offFileSet = 4 + 12 // returned.fileattr
		offName    = 4 + 20
		offDev     = offName + 8
		offType    = offDev + 4
		offMtime   = offType + 4
		offID      = offMtime + 16
		offLinks   = offID + 8
		offSize    = offLinks + 4
		offAlloc   = offSize + 8
		end        = offAlloc + 8
	)
	if len(r) < offLinks {
		return entry{}, false
	}
	le := binary.LittleEndian
	nameOff := int(int32(le.Uint32(r[offName:]))) //nolint:gosec // an offset inside the record
	nameLen := int(le.Uint32(r[offName+4:]))
	start := offName + nameOff
	if start < 0 || nameLen < 1 || start+nameLen > len(r) {
		return entry{}, false
	}
	name := string(r[start : start+nameLen-1]) // drop the NUL
	if name == "." || name == ".." {
		return entry{}, false
	}
	e := entry{
		name:  name,
		dev:   uint64(le.Uint32(r[offDev:])),
		ino:   le.Uint64(r[offID:]),
		mtime: int64(le.Uint64(r[offMtime:])), //nolint:gosec // time_t
	}
	if le.Uint32(r[offFileSet:])&unix.ATTR_FILE_ALLOCSIZE != 0 && len(r) >= end {
		e.nlink = uint64(le.Uint32(r[offLinks:]))
		e.size = int64(le.Uint64(r[offSize:]))   //nolint:gosec // off_t
		e.alloc = int64(le.Uint64(r[offAlloc:])) //nolint:gosec // off_t
	}
	switch le.Uint32(r[offType:]) {
	case vdir:
		e.kind = kindDir
	case vlnk:
		e.kind = kindLink
	case vreg:
		e.kind = kindFile
	default:
		e.kind = kindOther
	}
	return e, true
}
