// Package mp4fix repairs, at playback time and without touching the source
// file, MP4s that strict players (browsers, QuickTime) stutter on:
//
//   - "ctts": an H.264 track with B-frames but no composition offsets, so every
//     frame claims pts == dts and pictures are presented out of order. Display
//     order is recovered from the picture order count in each slice header.
//   - "faststart": the moov sits after the media data, so a browser has to
//     fetch the end of a multi-GB file before it can start.
//
// The result is a Plan: a rewritten header (ftyp + moov) followed by byte
// ranges of the original file, served as one virtual file. Sample data is
// never modified or copied.
package mp4fix

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

// ErrNotNeeded means the file plays fine as it is.
var ErrNotNeeded = errors.New("mp4fix: no fix needed")

type unsupportedError string

func (e unsupportedError) Error() string { return "mp4fix: unsupported: " + string(e) }

func errUnsupported(what string) error { return unsupportedError(what) }

// IsUnsupported reports whether err means "cannot fix this file" (as opposed to an I/O error).
func IsUnsupported(err error) bool {
	var u unsupportedError
	return errors.As(err, &u) || errors.Is(err, errBadBox) || errors.Is(err, errShortRBSP) ||
		errors.Is(err, errBadNAL) || errors.Is(err, errNoSlice)
}

// Fix reasons.
const (
	ReasonCtts      = "ctts"
	ReasonFaststart = "faststart"
)

// Part is a range of the source file.
type Part struct{ Src, Len int64 }

// Plan describes the virtual, fixed file: Header followed by Parts.
type Plan struct {
	Header  []byte
	Parts   []Part
	Size    int64
	Reasons []string
}

// maxMoov bounds the moov loaded into memory (a 3-hour 1080p file has ~15 MB).
const maxMoov = 256 << 20

// probeSamples is how many leading samples decide whether a track reorders
// at all, before paying for a full scan.
const probeSamples = 600

// Analyze inspects an MP4 and returns the plan for a fixed virtual file, or
// ErrNotNeeded.
func Analyze(r io.ReaderAt, size int64) (*Plan, error) {
	tops, err := topLevel(r, size)
	if err != nil {
		return nil, err
	}
	var moovTop *topBox
	firstMdat := int64(-1)
	for i, t := range tops {
		switch t.typ {
		case "moov":
			if moovTop != nil {
				return nil, errUnsupported("several moov boxes")
			}
			moovTop = &tops[i]
		case "mdat":
			if firstMdat < 0 {
				firstMdat = t.off
			}
		case "moof":
			return nil, errUnsupported("fragmented MP4")
		}
	}
	if moovTop == nil || firstMdat < 0 {
		return nil, errUnsupported("no moov or mdat")
	}
	if moovTop.size > maxMoov {
		return nil, errUnsupported("moov too large")
	}
	raw := make([]byte, moovTop.size)
	if _, err := r.ReadAt(raw, moovTop.off); err != nil {
		return nil, err
	}
	kids, err := parseBoxes(raw)
	if err != nil || len(kids) != 1 || kids[0].typ != "moov" {
		return nil, errBadBox
	}
	moov := kids[0]
	if moov.child("mvex") != nil {
		return nil, errUnsupported("fragmented MP4")
	}

	var reasons []string
	if moovTop.off > firstMdat {
		reasons = append(reasons, ReasonFaststart)
	}
	for _, trak := range moov.children {
		if trak.typ != "trak" || handler(trak) != "vide" {
			continue
		}
		fixed, err := addCtts(r, trak)
		if err != nil && !IsUnsupported(err) {
			return nil, err // I/O
		}
		if fixed {
			reasons = append(reasons, ReasonCtts)
		}
	}
	if len(reasons) == 0 {
		return nil, ErrNotNeeded
	}
	return layout(r, tops, moov, reasons)
}

func handler(trak *box) string {
	h := trak.find("mdia", "hdlr")
	if h == nil || len(h.data) < 12 {
		return ""
	}
	return string(h.data[8:12])
}

// avcConfig returns the NAL length size and SPS of an avc1/avc3 sample entry.
func avcConfig(stbl *box) (int, sps, error) {
	stsd := stbl.child("stsd")
	if stsd == nil || len(stsd.data) < 16 {
		return 0, sps{}, errBadBox
	}
	if binary.BigEndian.Uint32(stsd.data[4:]) != 1 {
		return 0, sps{}, errUnsupported("several sample descriptions")
	}
	entry := stsd.data[8:]
	if t := string(entry[4:8]); t != "avc1" && t != "avc3" {
		return 0, sps{}, errUnsupported("codec " + t)
	}
	i := indexOf(entry, "avcC")
	if i < 0 || i+4+8 > len(entry) {
		return 0, sps{}, errUnsupported("no avcC")
	}
	c := entry[i+4:]
	nalLen := int(c[4]&3) + 1
	if c[5]&0x1f == 0 {
		return 0, sps{}, errUnsupported("SPS only in-band")
	}
	n := int(binary.BigEndian.Uint16(c[6:]))
	if 8+n > len(c) {
		return 0, sps{}, errBadBox
	}
	s, err := parseSPS(c[8 : 8+n])
	return nalLen, s, err
}

func indexOf(b []byte, s string) int {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return i
		}
	}
	return -1
}

// addCtts computes composition offsets for a video track that lacks them and
// inserts a ctts box (and adjusts the edit list). It reports whether the
// track needed it.
func addCtts(r io.ReaderAt, trak *box) (bool, error) {
	stbl := trak.find("mdia", "minf", "stbl")
	if stbl == nil {
		return false, errBadBox
	}
	if stbl.child("ctts") != nil {
		return false, nil
	}
	nalLen, s, err := avcConfig(stbl)
	if err != nil {
		return false, err
	}
	if s.pocType == 2 || !s.frameMbsOnly {
		return false, nil // type 2 never reorders; interlaced streams are left alone
	}
	if stbl.child("stsz") == nil {
		return false, errUnsupported("compact sample sizes")
	}
	sizes, err := stszSizes(stbl.child("stsz"))
	if err != nil {
		return false, err
	}
	offs, err := sampleOffsets(stbl, sizes)
	if err != nil {
		return false, err
	}
	pr := newPicReader(r, nalLen, s)
	read := func(from, to int) ([]picture, error) {
		pics := make([]picture, 0, to-from)
		for i := from; i < to; i++ {
			p, err := pr.read(offs[i], int64(sizes[i]))
			if err != nil {
				return nil, err
			}
			pics = append(pics, p)
		}
		return pics, nil
	}
	// Cheap check first: most files either reorder right away or never.
	probe := min(len(sizes), probeSamples)
	pics, err := read(0, probe)
	if err != nil {
		return false, err
	}
	if !reorders(pocs(pics, s.log2MaxPocLsb), pics) {
		return false, nil
	}
	rest, err := read(probe, len(sizes))
	if err != nil {
		return false, err
	}
	pics = append(pics, rest...)

	deltas, err := sttsDeltas(stbl.child("stts"), len(sizes))
	if err != nil {
		return false, err
	}
	offsets, shift := compositionOffsets(pocs(pics, s.log2MaxPocLsb), pics, deltas)
	stbl.insertAfter("stts", cttsBox(offsets))
	return true, setEditShift(trak, shift)
}

// reorders reports whether presentation order differs from decoding order.
func reorders(poc []int64, pics []picture) bool {
	for i := 1; i < len(poc); i++ {
		if !pics[i].idr && poc[i] < poc[i-1] {
			return true
		}
	}
	return false
}

// compositionOffsets gives each sample pts-dts. Within an IDR period the k-th
// picture in POC order is presented at the k-th decoding timestamp, which
// keeps the timeline and durations intact. Offsets are shifted to be
// non-negative; the shift is returned for the edit list.
func compositionOffsets(poc []int64, pics []picture, deltas []uint32) ([]uint32, int64) {
	n := len(poc)
	dts := make([]int64, n)
	for i := 1; i < n; i++ {
		dts[i] = dts[i-1] + int64(deltas[i-1])
	}
	pts := make([]int64, n)
	order := make([]int, 0, 128)
	start := 0
	for i := 1; i <= n; i++ {
		if i < n && !pics[i].idr {
			continue
		}
		order = order[:0]
		for j := start; j < i; j++ {
			order = append(order, j)
		}
		sort.SliceStable(order, func(a, b int) bool { return poc[order[a]] < poc[order[b]] })
		for k, j := range order {
			pts[j] = dts[start+k]
		}
		start = i
	}
	var minOff int64
	for i := range pts {
		minOff = min(minOff, pts[i]-dts[i])
	}
	out := make([]uint32, n)
	for i := range pts {
		out[i] = uint32(pts[i] - dts[i] - minOff)
	}
	return out, -minOff
}

func cttsBox(offsets []uint32) *box {
	data := make([]byte, 8)
	runs := 0
	for i := 0; i < len(offsets); {
		j := i
		for j < len(offsets) && offsets[j] == offsets[i] {
			j++
		}
		data = binary.BigEndian.AppendUint32(data, uint32(j-i))
		data = binary.BigEndian.AppendUint32(data, offsets[i])
		runs++
		i = j
	}
	binary.BigEndian.PutUint32(data[4:], uint32(runs))
	return &box{typ: "ctts", data: data}
}

// setEditShift makes the track's edit list start at media time shift, so the
// first presented frame stays at time zero; it adds an edit list if needed.
func setEditShift(trak *box, shift int64) error {
	if elst := trak.find("edts", "elst"); elst != nil {
		d := elst.data
		if len(d) < 8 {
			return errBadBox
		}
		entry, size := 8, 12 // version 0: u32 duration, s32 media_time, rate
		if d[0] == 1 {
			size = 20
		}
		n := int(binary.BigEndian.Uint32(d[4:]))
		for i := 0; i < n && entry+size <= len(d); i, entry = i+1, entry+size {
			if d[0] == 1 {
				if int64(binary.BigEndian.Uint64(d[entry+8:])) == -1 {
					continue // empty edit
				}
				binary.BigEndian.PutUint64(d[entry+8:], uint64(shift))
			} else {
				if int32(binary.BigEndian.Uint32(d[entry+4:])) == -1 {
					continue
				}
				binary.BigEndian.PutUint32(d[entry+4:], uint32(shift))
			}
			return nil
		}
		return errUnsupported("edit list without media")
	}
	if shift == 0 {
		return nil
	}
	tkhd := trak.child("tkhd")
	if tkhd == nil || len(tkhd.data) < 36 {
		return errBadBox
	}
	var dur uint64
	if tkhd.data[0] == 1 {
		dur = binary.BigEndian.Uint64(tkhd.data[4+8+8+4+4:])
	} else {
		dur = uint64(binary.BigEndian.Uint32(tkhd.data[4+4+4+4+4:]))
	}
	d := make([]byte, 4, 28)
	d[0] = 1
	d = binary.BigEndian.AppendUint32(d, 1)
	d = binary.BigEndian.AppendUint64(d, dur)
	d = binary.BigEndian.AppendUint64(d, uint64(shift))
	d = binary.BigEndian.AppendUint32(d, 1<<16) // rate 1.0
	trak.insertAfter("tkhd", &box{typ: "edts", children: []*box{{typ: "elst", data: d}}})
	return nil
}

// layout puts ftyp and the new moov first, then every other top-level box in
// source order, and rewrites chunk offsets to their new positions.
func layout(r io.ReaderAt, tops []topBox, moov *box, reasons []string) (*Plan, error) {
	var ftyp []byte
	var rest []topBox
	for _, t := range tops {
		switch t.typ {
		case "ftyp":
			ftyp = make([]byte, t.size)
			if _, err := r.ReadAt(ftyp, t.off); err != nil {
				return nil, err
			}
		case "moov":
		default:
			rest = append(rest, t)
		}
	}
	type table struct {
		b    *box
		orig []int64
	}
	var tables []table
	for _, t := range moov.children {
		if t.typ != "trak" {
			continue
		}
		stbl := t.find("mdia", "minf", "stbl")
		if stbl == nil {
			return nil, errBadBox
		}
		b, offs, err := chunkOffsets(stbl)
		if err != nil {
			return nil, err
		}
		tables = append(tables, table{b, offs})
	}
	// Chunk offsets depend on the moov size, which can grow when stco must
	// become co64; a couple of rounds reach a fixed point.
	for round := 0; ; round++ {
		before := moov.size()
		pos := int64(len(ftyp)) + int64(before)
		shift := make([]int64, len(rest))
		for i, t := range rest {
			shift[i] = pos - t.off
			pos += t.size
		}
		for _, tb := range tables {
			moved := make([]int64, len(tb.orig))
			for k, o := range tb.orig {
				i := sort.Search(len(rest), func(i int) bool { return rest[i].off+rest[i].size > o })
				if i == len(rest) || o < rest[i].off {
					return nil, fmt.Errorf("%w: chunk offset outside the file's boxes", errBadBox)
				}
				moved[k] = o + shift[i]
			}
			setChunkOffsets(tb.b, moved)
		}
		if moov.size() == before {
			break
		}
		if round == 3 {
			return nil, errBadBox
		}
	}
	header := moov.encode(append([]byte{}, ftyp...))
	plan := &Plan{Header: header, Reasons: reasons, Size: int64(len(header))}
	for _, t := range rest {
		plan.Parts = append(plan.Parts, Part{Src: t.off, Len: t.size})
		plan.Size += t.size
	}
	return plan, nil
}
