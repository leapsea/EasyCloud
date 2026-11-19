package mp4fix

import (
	"errors"
	"io"
	"sort"
)

// File is the virtual fixed file: the plan's header followed by ranges of
// the source. It implements io.ReadSeeker for http.ServeContent.
type File struct {
	plan  *Plan
	src   io.ReaderAt
	start []int64 // virtual offset of each part
	pos   int64
}

// Open returns the virtual file of p over src (the unchanged original).
func (p *Plan) Open(src io.ReaderAt) *File {
	f := &File{plan: p, src: src, start: make([]int64, len(p.Parts))}
	off := int64(len(p.Header))
	for i, part := range p.Parts {
		f.start[i] = off
		off += part.Len
	}
	return f
}

func (f *File) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("mp4fix: negative offset")
	}
	n := 0
	for n < len(b) {
		at := off + int64(n)
		if at >= f.plan.Size {
			return n, io.EOF
		}
		if at < int64(len(f.plan.Header)) {
			n += copy(b[n:], f.plan.Header[at:])
			continue
		}
		i := sort.Search(len(f.start), func(i int) bool { return f.start[i] > at }) - 1
		part := f.plan.Parts[i]
		within := at - f.start[i]
		chunk := b[n:min(len(b), n+int(part.Len-within))]
		m, err := f.src.ReadAt(chunk, part.Src+within)
		n += m
		if err != nil && !(errors.Is(err, io.EOF) && m == len(chunk)) {
			return n, err
		}
	}
	return n, nil
}

func (f *File) Read(b []byte) (int, error) {
	n, err := f.ReadAt(b, f.pos)
	f.pos += int64(n)
	return n, err
}

func (f *File) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.pos
	case io.SeekEnd:
		offset += f.plan.Size
	default:
		return 0, errors.New("mp4fix: bad whence")
	}
	if offset < 0 {
		return 0, errors.New("mp4fix: negative position")
	}
	f.pos = offset
	return offset, nil
}
