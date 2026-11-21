package app

import (
	"archive/zip"
	"context"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/easycloud/easycloud/internal/pathguard"
)

// Already-compressed formats are stored as-is: deflating them only costs CPU.
var storedExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".heic": true, ".avif": true,
	".mp4": true, ".mkv": true, ".mov": true, ".avi": true, ".webm": true, ".mp3": true, ".aac": true, ".flac": true, ".m4a": true, ".ogg": true,
	".zip": true, ".gz": true, ".xz": true, ".7z": true, ".rar": true, ".zst": true, ".bz2": true,
	".docx": true, ".xlsx": true, ".pptx": true, ".pdf": true,
}

// WriteZip streams the given paths (files or directories) as one zip archive.
// Every read goes through the caller's grant roots; symlinks are skipped.
func (s *FileService) WriteZip(ctx context.Context, sub pathguard.Subject, paths []string, w io.Writer) error {
	type item struct {
		p pathguard.RelPath
		h pathguard.Handle
	}
	var items []item
	defer func() {
		for _, it := range items {
			it.h.Close()
		}
	}()
	for _, raw := range paths {
		p, h, err := s.check(ctx, sub, raw, pathguard.PermRead)
		if err != nil {
			return err
		}
		items = append(items, item{p, h})
	}
	zw := zip.NewWriter(w)
	used := map[string]bool{}
	for _, it := range items {
		base := it.p.Name()
		if base == "" {
			base = "files"
		}
		for i := 2; used[base]; i++ { // two selected items with the same name
			base = strings.TrimSuffix(it.p.Name(), path.Ext(it.p.Name())) + " (" + itoa(i) + ")" + path.Ext(it.p.Name())
		}
		used[base] = true
		err := fs.WalkDir(it.h.Root.FS(), it.h.Rel, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped, not fatal
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			name := base
			switch {
			case p == it.h.Rel:
			case it.h.Rel == ".":
				name = base + "/" + p
			default:
				name = base + "/" + strings.TrimPrefix(p, it.h.Rel+"/")
			}
			if d.IsDir() && pathguard.IsReservedName(d.Name()) {
				return fs.SkipDir // trash of a mounted folder, at any depth
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if d.IsDir() {
				_, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Modified: info.ModTime(), Method: zip.Store})
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			hdr.Name = name
			hdr.Method = zip.Deflate
			if storedExt[strings.ToLower(path.Ext(name))] {
				hdr.Method = zip.Store
			}
			dst, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			f, err := it.h.Root.Open(p)
			if err != nil {
				return nil
			}
			_, err = io.Copy(dst, f)
			f.Close()
			return err
		})
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}
