package mp4fix

import (
	"encoding/binary"
	"io"
)

// Track describes one track of an MP4, as read from its moov (no decoding).
type Track struct {
	Kind       string  // "video", "audio" or the raw handler type
	Codec      string  // sample entry type, e.g. avc1, hvc1, mp4a
	Width      int     // video
	Height     int     // video
	FPS        float64 // video: samples per second of media time
	SampleRate int     // audio
	Channels   int     // audio
	Bitrate    int64   // bits per second
	Duration   float64 // seconds
}

// Movie is what Inspect reports about a file.
type Movie struct {
	Duration float64
	Tracks   []Track
}

// Inspect reads basic stream information from an MP4/MOV's moov. It is the
// fallback when ffprobe is not installed.
func Inspect(r io.ReaderAt, size int64) (Movie, error) {
	tops, err := topLevel(r, size)
	if err != nil {
		return Movie{}, err
	}
	var mt *topBox
	for i := range tops {
		if tops[i].typ == "moov" {
			mt = &tops[i]
		}
	}
	if mt == nil || mt.size > maxMoov {
		return Movie{}, errUnsupported("no usable moov")
	}
	raw := make([]byte, mt.size)
	if _, err := r.ReadAt(raw, mt.off); err != nil {
		return Movie{}, err
	}
	kids, err := parseBoxes(raw)
	if err != nil || len(kids) != 1 {
		return Movie{}, errBadBox
	}
	moov := kids[0]
	var m Movie
	if d, ts, ok := durationOf(moov.child("mvhd")); ok && ts > 0 {
		m.Duration = float64(d) / float64(ts)
	}
	for _, trak := range moov.children {
		if trak.typ != "trak" {
			continue
		}
		t := Track{Kind: handler(trak)}
		switch t.Kind {
		case "vide":
			t.Kind = "video"
		case "soun":
			t.Kind = "audio"
		}
		if d, ts, ok := durationOf(trak.find("mdia", "mdhd")); ok && ts > 0 {
			t.Duration = float64(d) / float64(ts)
		}
		stbl := trak.find("mdia", "minf", "stbl")
		if stbl == nil {
			continue
		}
		if stsd := stbl.child("stsd"); stsd != nil && len(stsd.data) >= 16 {
			e := stsd.data[8:]
			t.Codec = string(e[4:8])
			switch t.Kind {
			case "video":
				if len(e) >= 36 {
					t.Width, t.Height = int(binary.BigEndian.Uint16(e[32:])), int(binary.BigEndian.Uint16(e[34:]))
				}
			case "audio":
				if len(e) >= 36 {
					t.Channels = int(binary.BigEndian.Uint16(e[24:]))
					t.SampleRate = int(binary.BigEndian.Uint32(e[32:]) >> 16)
				}
			}
		}
		if stsz := stbl.child("stsz"); stsz != nil && t.Duration > 0 {
			if sizes, err := stszSizes(stsz); err == nil {
				var total int64
				for _, s := range sizes {
					total += int64(s)
				}
				t.Bitrate = int64(float64(total*8) / t.Duration)
				if t.Kind == "video" {
					t.FPS = float64(len(sizes)) / t.Duration
				}
			}
		}
		m.Tracks = append(m.Tracks, t)
	}
	return m, nil
}

// durationOf reads duration and timescale from an mvhd or mdhd box.
func durationOf(b *box) (uint64, uint32, bool) {
	if b == nil || len(b.data) < 24 {
		return 0, 0, false
	}
	if b.data[0] == 1 {
		if len(b.data) < 32 {
			return 0, 0, false
		}
		return binary.BigEndian.Uint64(b.data[24:]), binary.BigEndian.Uint32(b.data[20:]), true
	}
	return uint64(binary.BigEndian.Uint32(b.data[16:])), binary.BigEndian.Uint32(b.data[12:]), true
}
