// Package info answers "file details" for the file manager: timestamps
// including creation time, and media properties (resolution, frame rate,
// codecs, duration, ...) for video, audio and images.
package info

import (
	"context"
	"encoding/json"
	"image"
	_ "image/gif" // registered for image.DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/easycloud/easycloud/internal/media/mp4fix"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/easycloud/easycloud/internal/storage/app"
	"github.com/easycloud/easycloud/internal/storage/domain"
	"github.com/go-chi/chi/v5"
)

// probeTimeout bounds ffprobe; it only reads container headers, so this is generous.
const probeTimeout = 15 * time.Second

type Details struct {
	domain.FileEntry
	// CreatedAt is the file's birth time when the filesystem records it.
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	Media     *Media     `json:"media,omitempty"`
	Image     *ImageInfo `json:"image,omitempty"`
}

type Media struct {
	Source    string  `json:"source"` // "ffprobe" or "builtin"
	Container string  `json:"container,omitempty"`
	Duration  float64 `json:"duration,omitempty"` // seconds
	Bitrate   int64   `json:"bitrate,omitempty"`  // bits per second, whole file
	Video     *Video  `json:"video,omitempty"`
	Audio     *Audio  `json:"audio,omitempty"`
}

type Video struct {
	Codec   string  `json:"codec,omitempty"`
	Profile string  `json:"profile,omitempty"`
	Width   int     `json:"width,omitempty"`
	Height  int     `json:"height,omitempty"`
	FPS     float64 `json:"fps,omitempty"`
	Bitrate int64   `json:"bitrate,omitempty"`
	PixFmt  string  `json:"pixFmt,omitempty"`
}

type Audio struct {
	Codec      string `json:"codec,omitempty"`
	SampleRate int    `json:"sampleRate,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	Bitrate    int64  `json:"bitrate,omitempty"`
}

type ImageInfo struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Format string `json:"format"`
}

type Service struct {
	files *app.FileService
	log   *slog.Logger

	once    sync.Once
	ffprobe string // empty when not installed
}

func New(files *app.FileService, log *slog.Logger) *Service {
	return &Service{files: files, log: log}
}

// Mount registers GET /files/details?path= (caller applies authentication).
func Mount(r chi.Router, s *Service, log *slog.Logger) {
	r.Get("/files/details", httpx.Handler(s.details).With(log))
}

func (s *Service) details(w http.ResponseWriter, r *http.Request) error {
	f, e, err := s.files.OpenEntry(r.Context(), httpx.Subject(r), r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	defer f.Close()
	d := Details{FileEntry: e}
	if fi, err := f.Stat(); err == nil {
		if t, ok := birthTime(f, fi); ok {
			d.CreatedAt = &t
		}
	}
	if !e.IsDir {
		switch {
		case strings.HasPrefix(e.Mime, "video/"), strings.HasPrefix(e.Mime, "audio/"):
			d.Media = s.media(r.Context(), f, e)
		case strings.HasPrefix(e.Mime, "image/"):
			if cfg, format, err := image.DecodeConfig(f); err == nil {
				d.Image = &ImageInfo{Width: cfg.Width, Height: cfg.Height, Format: format}
			} else if e.Width > 0 {
				d.Image = &ImageInfo{Width: e.Width, Height: e.Height}
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, d)
	return nil
}

func (s *Service) media(ctx context.Context, f *os.File, e domain.FileEntry) *Media {
	s.once.Do(func() { s.ffprobe, _ = exec.LookPath("ffprobe") })
	if s.ffprobe != "" {
		if m, err := s.probe(ctx, f); err == nil {
			return m
		} else {
			s.log.Debug("ffprobe failed", "path", e.Path, "err", err)
		}
	}
	switch strings.ToLower(filepath.Ext(e.Name)) {
	case ".mp4", ".m4v", ".mov", ".m4a", ".3gp":
		if m, err := mp4fix.Inspect(f, e.Size); err == nil {
			return fromMovie(m, e.Size)
		}
	}
	return nil
}

// probe runs ffprobe on the already-opened file through /dev/fd/3, so the
// path never leaves the confined os.Root handle.
func (s *Service) probe(ctx context.Context, f *os.File) (*Media, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.ffprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", "/dev/fd/3")
	cmd.ExtraFiles = []*os.File{f}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var p struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType    string `json:"codec_type"`
			CodecName    string `json:"codec_name"`
			Profile      string `json:"profile"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			PixFmt       string `json:"pix_fmt"`
			AvgFrameRate string `json:"avg_frame_rate"`
			RFrameRate   string `json:"r_frame_rate"`
			BitRate      string `json:"bit_rate"`
			SampleRate   string `json:"sample_rate"`
			Channels     int    `json:"channels"`
			Disposition  struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, err
	}
	m := &Media{Source: "ffprobe", Container: p.Format.FormatName, Duration: atof(p.Format.Duration), Bitrate: atoi(p.Format.BitRate)}
	for _, st := range p.Streams {
		switch {
		case st.CodecType == "video" && m.Video == nil && st.Disposition.AttachedPic == 0: // skip cover art
			fps := rate(st.AvgFrameRate)
			if fps == 0 {
				fps = rate(st.RFrameRate)
			}
			m.Video = &Video{Codec: st.CodecName, Profile: st.Profile, Width: st.Width, Height: st.Height, FPS: fps, Bitrate: atoi(st.BitRate), PixFmt: st.PixFmt}
		case st.CodecType == "audio" && m.Audio == nil:
			m.Audio = &Audio{Codec: st.CodecName, SampleRate: int(atoi(st.SampleRate)), Channels: st.Channels, Bitrate: atoi(st.BitRate)}
		}
	}
	return m, nil
}

func fromMovie(mv mp4fix.Movie, size int64) *Media {
	m := &Media{Source: "builtin", Container: "mp4", Duration: mv.Duration}
	if mv.Duration > 0 {
		m.Bitrate = int64(float64(size*8) / mv.Duration)
	}
	for _, t := range mv.Tracks {
		switch {
		case t.Kind == "video" && m.Video == nil:
			m.Video = &Video{Codec: t.Codec, Width: t.Width, Height: t.Height, FPS: t.FPS, Bitrate: t.Bitrate}
		case t.Kind == "audio" && m.Audio == nil:
			m.Audio = &Audio{Codec: t.Codec, SampleRate: t.SampleRate, Channels: t.Channels, Bitrate: t.Bitrate}
		}
	}
	return m
}

func atof(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
func atoi(s string) int64   { v, _ := strconv.ParseInt(s, 10, 64); return v }

// rate parses ffprobe's "30000/1001" frame rates.
func rate(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return atof(s)
	}
	d := atof(den)
	if d == 0 {
		return 0
	}
	return math.Round(atof(num)/d*1000) / 1000
}
