package mp4fix

import (
	"errors"
	"io"
)

// bitReader reads an H.264 RBSP (emulation prevention bytes already removed).
type bitReader struct {
	b   []byte
	pos int
	err error
}

func newBitReader(nal []byte) *bitReader {
	out := make([]byte, 0, len(nal))
	zeros := 0
	for _, x := range nal {
		if zeros >= 2 && x == 3 { // emulation_prevention_three_byte
			zeros = 0
			continue
		}
		out = append(out, x)
		if x == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return &bitReader{b: out}
}

var (
	errShortRBSP = errors.New("mp4fix: truncated H.264 header")
	errBadNAL    = errors.New("mp4fix: bad NAL length")
	errNoSlice   = errors.New("mp4fix: sample without a slice")
)

func (r *bitReader) u(n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		if r.pos>>3 >= len(r.b) {
			r.err = errShortRBSP
			return 0
		}
		v = v<<1 | uint32(r.b[r.pos>>3]>>(7-r.pos&7))&1
		r.pos++
	}
	return v
}

func (r *bitReader) ue() uint32 {
	zeros := 0
	for r.u(1) == 0 {
		if r.err != nil || zeros > 31 {
			r.err = errShortRBSP
			return 0
		}
		zeros++
	}
	return 1<<zeros - 1 + r.u(zeros)
}

// sps holds the fields needed to read pic_order_cnt_lsb from slice headers.
type sps struct {
	log2MaxFrameNum  int
	pocType          uint32
	log2MaxPocLsb    int
	frameMbsOnly     bool
	separateColPlane bool
}

// highProfiles carry chroma/bit-depth fields before log2_max_frame_num.
var highProfiles = map[uint32]bool{100: true, 110: true, 122: true, 244: true, 44: true, 83: true, 86: true, 118: true, 128: true, 138: true, 139: true, 134: true, 135: true}

// parseSPS parses a sequence parameter set NAL unit (including its header byte).
func parseSPS(nal []byte) (sps, error) {
	if len(nal) < 4 {
		return sps{}, errShortRBSP
	}
	r := newBitReader(nal[1:])
	var s sps
	profile := r.u(8)
	r.u(16) // constraint flags, level
	r.ue()  // seq_parameter_set_id
	if highProfiles[profile] {
		if r.ue() == 3 { // chroma_format_idc
			s.separateColPlane = r.u(1) == 1
		}
		r.ue() // bit_depth_luma_minus8
		r.ue() // bit_depth_chroma_minus8
		r.u(1) // qpprime_y_zero_transform_bypass_flag
		if r.u(1) == 1 {
			return sps{}, errUnsupported("SPS scaling matrices")
		}
	}
	s.log2MaxFrameNum = int(r.ue()) + 4
	s.pocType = r.ue()
	switch s.pocType {
	case 0:
		s.log2MaxPocLsb = int(r.ue()) + 4
	case 2:
	default:
		return sps{}, errUnsupported("pic_order_cnt_type 1")
	}
	r.ue() // max_num_ref_frames
	r.u(1) // gaps_in_frame_num_value_allowed_flag
	r.ue() // pic_width_in_mbs_minus1
	r.ue() // pic_height_in_map_units_minus1
	s.frameMbsOnly = r.u(1) == 1
	return s, r.err
}

// picture is what the composition-order calculation needs from one sample.
type picture struct {
	idr bool
	ref bool   // nal_ref_idc != 0
	lsb uint32 // pic_order_cnt_lsb
}

// picReader parses the first slice header of each sample. Samples are read
// through a large window that moves forward through the file: video samples
// sit close together in increasing order, so the analysis becomes one
// sequential pass instead of ~330k small random reads (which cost ~18 s on
// an SSD and would take far longer on a NAS hard disk).
type picReader struct {
	r      io.ReaderAt
	nalLen int
	s      sps
	win    []byte
	winOff int64
	winLen int
	small  []byte
}

const picWindow = 8 << 20

func newPicReader(r io.ReaderAt, nalLen int, s sps) *picReader {
	return &picReader{r: r, nalLen: nalLen, s: s, win: make([]byte, picWindow), winOff: -1, small: make([]byte, 64)}
}

// at returns n bytes at file offset p: from the window, refilling it forward
// when p is ahead of it, or with a single read for anything else.
func (pr *picReader) at(p int64, n int) ([]byte, error) {
	in := func() bool { return pr.winOff >= 0 && p >= pr.winOff && p+int64(n) <= pr.winOff+int64(pr.winLen) }
	if !in() && p >= pr.winOff {
		m, err := pr.r.ReadAt(pr.win, p)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		pr.winOff, pr.winLen = p, m
	}
	if in() {
		return pr.win[p-pr.winOff : p-pr.winOff+int64(n)], nil
	}
	b := pr.small[:n]
	if _, err := pr.r.ReadAt(b, p); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return b, nil
}

func (pr *picReader) read(off, size int64) (picture, error) {
	end := off + size
	for p := off; p+int64(pr.nalLen) <= end; {
		lb, err := pr.at(p, pr.nalLen)
		if err != nil {
			return picture{}, err
		}
		var n int64
		for _, b := range lb {
			n = n<<8 | int64(b)
		}
		p += int64(pr.nalLen)
		if n <= 0 || p+n > end {
			return picture{}, errBadNAL
		}
		h, err := pr.at(p, int(min(n, 64)))
		if err != nil {
			return picture{}, err
		}
		p += n
		typ := h[0] & 0x1f
		if typ != 1 && typ != 5 { // not a slice of a (non-)IDR picture
			continue
		}
		br := newBitReader(h[1:])
		br.ue() // first_mb_in_slice
		br.ue() // slice_type
		br.ue() // pic_parameter_set_id
		if pr.s.separateColPlane {
			br.u(2) // colour_plane_id
		}
		br.u(pr.s.log2MaxFrameNum) // frame_num
		if typ == 5 {
			br.ue() // idr_pic_id
		}
		pic := picture{idr: typ == 5, ref: h[0]>>5&3 != 0, lsb: br.u(pr.s.log2MaxPocLsb)}
		return pic, br.err
	}
	return picture{}, errNoSlice
}

// pocs turns pic_order_cnt_lsb values (in decoding order) into full picture
// order counts, following H.264 8.2.1.1 for pic_order_cnt_type 0.
func pocs(pics []picture, log2MaxLsb int) []int64 {
	maxLsb := int64(1) << log2MaxLsb
	out := make([]int64, len(pics))
	var prevMsb, prevLsb int64
	for i, p := range pics {
		if p.idr {
			prevMsb, prevLsb = 0, 0
		}
		lsb := int64(p.lsb)
		msb := prevMsb
		switch {
		case lsb < prevLsb && prevLsb-lsb >= maxLsb/2:
			msb = prevMsb + maxLsb
		case lsb > prevLsb && lsb-prevLsb > maxLsb/2:
			msb = prevMsb - maxLsb
		}
		out[i] = msb + lsb
		if p.ref {
			prevMsb, prevLsb = msb, lsb
		}
	}
	return out
}
