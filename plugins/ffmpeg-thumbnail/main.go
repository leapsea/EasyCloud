// Command ffmpeg-thumbnail is an EasyCloud backend plugin that renders
// thumbnails for videos, HEIC/HEIF photos and camera RAW files with ffmpeg.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/easycloud/easycloud/pkg/pluginsdk"
)

type config struct {
	FFmpegPath string `json:"ffmpegPath"`
	SeekSec    int    `json:"seekSeconds"`
}

type ext struct {
	pluginsdk.Base
	cfg config
}

func (e ext) ffmpeg() string {
	if e.cfg.FFmpegPath != "" {
		return e.cfg.FFmpegPath
	}
	return "ffmpeg"
}

func (e ext) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, e.ffmpeg(), args...)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if out.Len() == 0 {
		return nil, errors.New("ffmpeg produced no image")
	}
	return out.Bytes(), nil
}

func (e ext) Thumbnail(ctx context.Context, req *pluginsdk.ThumbnailRequest) (*pluginsdk.ThumbnailResponse, error) {
	size := req.Size
	if size <= 0 || size > 2048 {
		size = 256
	}
	scale := fmt.Sprintf("scale='if(gt(iw,ih),min(%d,iw),-2)':'if(gt(iw,ih),-2,min(%d,ih))'", size, size)
	common := []string{"-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "4", "-"}
	var attempts [][]string
	if strings.HasPrefix(req.Mime, "video/") {
		seek := e.cfg.SeekSec
		if seek <= 0 {
			seek = 3
		}
		// "thumbnail" picks a representative frame; fall back to the first frame for short clips.
		attempts = append(attempts,
			append([]string{"-hide_banner", "-loglevel", "error", "-ss", fmt.Sprint(seek), "-i", req.Path, "-vf", "thumbnail," + scale}, common...),
			append([]string{"-hide_banner", "-loglevel", "error", "-i", req.Path, "-vf", scale}, common...))
	} else {
		attempts = append(attempts, append([]string{"-hide_banner", "-loglevel", "error", "-i", req.Path, "-vf", scale}, common...))
	}
	var lastErr error
	for _, a := range attempts {
		img, err := e.run(ctx, a...)
		if err == nil {
			return &pluginsdk.ThumbnailResponse{Image: img, Mime: "image/jpeg"}, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func main() {
	var e ext
	_ = pluginsdk.Config(&e.cfg)
	pluginsdk.Serve(e)
}
