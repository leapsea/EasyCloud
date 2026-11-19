package mp4fix

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// box is an ISO BMFF box held in memory (only the moov is ever loaded).
type box struct {
	typ      string
	data     []byte // payload of a leaf box
	children []*box // non-nil for container boxes
}

var containerTypes = map[string]bool{"moov": true, "trak": true, "mdia": true, "minf": true, "stbl": true, "edts": true, "dinf": true}

var errBadBox = errors.New("mp4fix: malformed box")

func parseBoxes(b []byte) ([]*box, error) {
	var out []*box
	for len(b) > 0 {
		if len(b) < 8 {
			return nil, errBadBox
		}
		size, hdr := uint64(binary.BigEndian.Uint32(b)), 8
		typ := string(b[4:8])
		switch size {
		case 1:
			if len(b) < 16 {
				return nil, errBadBox
			}
			size, hdr = binary.BigEndian.Uint64(b[8:]), 16
		case 0:
			size = uint64(len(b))
		}
		if size < uint64(hdr) || size > uint64(len(b)) {
			return nil, errBadBox
		}
		payload := b[hdr:size]
		bx := &box{typ: typ}
		if containerTypes[typ] {
			kids, err := parseBoxes(payload)
			if err != nil {
				return nil, err
			}
			bx.children = append([]*box{}, kids...)
		} else {
			bx.data = payload
		}
		out = append(out, bx)
		b = b[size:]
	}
	return out, nil
}

func (b *box) size() uint64 {
	n := uint64(len(b.data))
	if b.children != nil {
		n = 0
		for _, c := range b.children {
			n += c.size()
		}
	}
	if n+8 > 0xFFFFFFFF {
		return n + 16
	}
	return n + 8
}

func (b *box) encode(out []byte) []byte {
	n := b.size()
	if n > 0xFFFFFFFF {
		out = binary.BigEndian.AppendUint32(out, 1)
		out = append(out, b.typ...)
		out = binary.BigEndian.AppendUint64(out, n)
	} else {
		out = binary.BigEndian.AppendUint32(out, uint32(n))
		out = append(out, b.typ...)
	}
	if b.children == nil {
		return append(out, b.data...)
	}
	for _, c := range b.children {
		out = c.encode(out)
	}
	return out
}

func (b *box) child(typ string) *box {
	for _, c := range b.children {
		if c.typ == typ {
			return c
		}
	}
	return nil
}

func (b *box) find(path ...string) *box {
	n := b
	for _, t := range path {
		if n = n.child(t); n == nil {
			return nil
		}
	}
	return n
}

// insertAfter puts c right after the first child of type after (or at the end).
func (b *box) insertAfter(after string, c *box) {
	for i, k := range b.children {
		if k.typ == after {
			b.children = append(b.children[:i+1], append([]*box{c}, b.children[i+1:]...)...)
			return
		}
	}
	b.children = append(b.children, c)
}

// topBox is a top-level box located in the source file.
type topBox struct {
	typ       string
	off, size int64
}

func topLevel(r io.ReaderAt, fileSize int64) ([]topBox, error) {
	var out []topBox
	hdr := make([]byte, 16)
	for off := int64(0); off < fileSize; {
		n, err := r.ReadAt(hdr, off)
		if n < 8 {
			if err == nil {
				err = errBadBox
			}
			return nil, fmt.Errorf("mp4fix: box header at %d: %w", off, err)
		}
		size := int64(binary.BigEndian.Uint32(hdr))
		switch size {
		case 1:
			if n < 16 {
				return nil, errBadBox
			}
			size = int64(binary.BigEndian.Uint64(hdr[8:]))
		case 0:
			size = fileSize - off
		}
		if size < 8 || off+size > fileSize {
			return nil, errBadBox
		}
		out = append(out, topBox{typ: string(hdr[4:8]), off: off, size: size})
		off += size
		if len(out) > 1024 {
			return nil, errUnsupported("too many top-level boxes")
		}
	}
	return out, nil
}

// ---------- sample tables ----------

func fullBoxBody(b *box) ([]byte, byte, error) {
	if b == nil || len(b.data) < 8 {
		return nil, 0, errBadBox
	}
	return b.data[4:], b.data[0], nil
}

func sttsDeltas(stts *box, n int) ([]uint32, error) {
	d, _, err := fullBoxBody(stts)
	if err != nil {
		return nil, err
	}
	cnt := int(binary.BigEndian.Uint32(d))
	if len(d) < 4+8*cnt {
		return nil, errBadBox
	}
	out := make([]uint32, 0, n)
	for i := 0; i < cnt; i++ {
		c := binary.BigEndian.Uint32(d[4+8*i:])
		delta := binary.BigEndian.Uint32(d[8+8*i:])
		for j := uint32(0); j < c && len(out) < n; j++ {
			out = append(out, delta)
		}
	}
	if len(out) != n {
		return nil, errBadBox
	}
	return out, nil
}

func stszSizes(stsz *box) ([]uint32, error) {
	d, _, err := fullBoxBody(stsz)
	if err != nil {
		return nil, err
	}
	uniform, n := binary.BigEndian.Uint32(d), int(binary.BigEndian.Uint32(d[4:]))
	out := make([]uint32, n)
	if uniform != 0 {
		for i := range out {
			out[i] = uniform
		}
		return out, nil
	}
	if len(d) < 8+4*n {
		return nil, errBadBox
	}
	for i := range out {
		out[i] = binary.BigEndian.Uint32(d[8+4*i:])
	}
	return out, nil
}

// chunkOffsets returns the stco/co64 box of a sample table and its offsets.
func chunkOffsets(stbl *box) (*box, []int64, error) {
	b := stbl.child("co64")
	wide := b != nil
	if !wide {
		b = stbl.child("stco")
	}
	d, _, err := fullBoxBody(b)
	if err != nil {
		return nil, nil, err
	}
	n := int(binary.BigEndian.Uint32(d))
	w := 4
	if wide {
		w = 8
	}
	if len(d) < 4+w*n {
		return nil, nil, errBadBox
	}
	out := make([]int64, n)
	for i := range out {
		if wide {
			out[i] = int64(binary.BigEndian.Uint64(d[4+8*i:]))
		} else {
			out[i] = int64(binary.BigEndian.Uint32(d[4+4*i:]))
		}
	}
	return b, out, nil
}

func setChunkOffsets(b *box, offs []int64) {
	wide := b.typ == "co64"
	for _, o := range offs {
		if o > 0xFFFFFFFF {
			wide = true
		}
	}
	data := make([]byte, 8, 8+8*len(offs))
	binary.BigEndian.PutUint32(data[4:], uint32(len(offs)))
	for _, o := range offs {
		if wide {
			data = binary.BigEndian.AppendUint64(data, uint64(o))
		} else {
			data = binary.BigEndian.AppendUint32(data, uint32(o))
		}
	}
	b.typ, b.data = "stco", data
	if wide {
		b.typ = "co64"
	}
}

// sampleOffsets resolves every sample's file offset from stsc + chunk offsets.
func sampleOffsets(stbl *box, sizes []uint32) ([]int64, error) {
	d, _, err := fullBoxBody(stbl.child("stsc"))
	if err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(d))
	if len(d) < 4+12*n || n == 0 {
		return nil, errBadBox
	}
	_, chunks, err := chunkOffsets(stbl)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(sizes))
	for i := 0; i < n; i++ {
		first := int(binary.BigEndian.Uint32(d[4+12*i:]))
		per := int(binary.BigEndian.Uint32(d[8+12*i:]))
		last := len(chunks)
		if i+1 < n {
			last = int(binary.BigEndian.Uint32(d[4+12*(i+1):])) - 1
		}
		if first < 1 || last > len(chunks) {
			return nil, errBadBox
		}
		for c := first; c <= last; c++ {
			off := chunks[c-1]
			for k := 0; k < per; k++ {
				if len(out) == len(sizes) {
					return nil, errBadBox
				}
				out = append(out, off)
				off += int64(sizes[len(out)-1])
			}
		}
	}
	if len(out) != len(sizes) {
		return nil, errBadBox
	}
	return out, nil
}
