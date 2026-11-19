package app

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
)

const (
	maxExtractEntries = 200_000
	maxExtractBytes   = 1 << 40 // 1 TB: guards against zip bombs beyond the quota check
)

// ArchiveKind returns "zip", "tar", "tgz" or "" for unsupported names.
func ArchiveKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".zip"):
		return "zip"
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		return "tgz"
	case strings.HasSuffix(n, ".tar"):
		return "tar"
	}
	return ""
}

func archiveBase(name string) string {
	n := name
	for _, ext := range []string{".tar.gz", ".tgz", ".zip", ".tar"} {
		if strings.HasSuffix(strings.ToLower(n), ext) {
			return n[:len(n)-len(ext)]
		}
	}
	return n
}

type entry struct {
	name  string
	dir   bool
	mode  os.FileMode
	size  int64
	open  func() (io.ReadCloser, error)
	isReg bool
}

// Extract unpacks an archive into a new folder next to it (named after the
// archive, renamed on conflict) and returns that folder's display path. Every
// entry name is validated with pathguard.Parse and written through the grant
// root, so "../" entries, absolute paths and symlinks can never escape (Zip Slip).
func (s *FileService) Extract(ctx context.Context, sub pathguard.Subject, raw string) (string, error) {
	src, sh, err := s.check(ctx, sub, raw, pathguard.PermRead)
	if err != nil {
		return "", err
	}
	defer sh.Close()
	kind := ArchiveKind(src.Name())
	if kind == "" {
		return "", errs.ErrInvalidInput.WithDetail("unsupported archive type (zip, tar, tar.gz)")
	}
	f, err := sh.Root.Open(sh.Rel)
	if err != nil {
		return "", mapFSErr(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return "", errs.ErrNotFound
	}

	dest, err := src.Parent().Join(archiveBase(src.Name()))
	if err != nil {
		return "", errs.ErrInvalidPath
	}
	dh, err := s.guard.Check(ctx, sub, dest, pathguard.PermWrite)
	if err != nil {
		return "", errs.ErrNotFound
	}
	defer dh.Close()
	dest, dh2, _, err := s.resolveTarget(dh, dest, ConflictRename, true)
	if err != nil {
		return "", err
	}
	if err := dh2.Root.Mkdir(dh2.Rel, 0o755); err != nil {
		return "", mapFSErr(err)
	}
	root := dh2.Rel

	var entries []entry
	var total int64
	switch kind {
	case "zip":
		zr, err := zip.NewReader(f, fi.Size())
		if err != nil {
			return "", errs.ErrInvalidInput.WithDetail("not a valid zip archive")
		}
		for _, zf := range zr.File {
			zf := zf
			entries = append(entries, entry{name: zf.Name, dir: zf.FileInfo().IsDir(), mode: zf.Mode(), size: int64(zf.UncompressedSize64),
				isReg: zf.Mode().IsRegular(), open: func() (io.ReadCloser, error) { return zf.Open() }})
			total += int64(zf.UncompressedSize64)
		}
	default:
		// Tar is streamed: entries are written as they are read.
		var r io.Reader = f
		if kind == "tgz" {
			gz, err := gzip.NewReader(f)
			if err != nil {
				return "", errs.ErrInvalidInput.WithDetail("not a valid gzip stream")
			}
			defer gz.Close()
			r = gz
		}
		tr := tar.NewReader(r)
		n := 0
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return dest.Display(), errs.ErrInvalidInput.WithDetail("corrupt tar archive")
			}
			if n++; n > maxExtractEntries {
				return dest.Display(), errs.ErrInvalidInput.WithDetail("too many entries")
			}
			total += hdr.Size
			if total > maxExtractBytes {
				return dest.Display(), errs.ErrInvalidInput.WithDetail("archive too large")
			}
			e := entry{name: hdr.Name, dir: hdr.Typeflag == tar.TypeDir, mode: os.FileMode(hdr.Mode).Perm(), size: hdr.Size,
				isReg: hdr.Typeflag == tar.TypeReg, open: func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }}
			if err := s.quotaForEntry(ctx, sub, dest, e.size); err != nil {
				return dest.Display(), err
			}
			if err := writeEntry(ctx, dh2.Root, root, e); err != nil {
				return dest.Display(), err
			}
		}
		s.publish("created", dest.Parent())
		return dest.Display(), nil
	}

	if len(entries) > maxExtractEntries || total > maxExtractBytes {
		return dest.Display(), errs.ErrInvalidInput.WithDetail("archive too large")
	}
	if err := s.checkQuota(ctx, sub, dest, total); err != nil {
		_ = dh2.Root.RemoveAll(root)
		return "", err
	}
	if p := progressFrom(ctx); p != nil {
		p(0)
	}
	for _, e := range entries {
		if err := writeEntry(ctx, dh2.Root, root, e); err != nil {
			return dest.Display(), err
		}
	}
	s.publish("created", dest.Parent())
	return dest.Display(), nil
}

func (s *FileService) quotaForEntry(ctx context.Context, sub pathguard.Subject, dest pathguard.RelPath, n int64) error {
	if n <= 0 {
		return nil
	}
	return s.checkQuota(ctx, sub, dest, n)
}

// writeEntry validates one archive entry and writes it below root.
func writeEntry(ctx context.Context, r *os.Root, root string, e entry) error {
	name := strings.TrimSuffix(strings.ReplaceAll(e.name, "\\", "/"), "/")
	if name == "" || name == "." {
		return nil
	}
	rel, err := pathguard.Parse(name)
	if err != nil || rel.IsRoot() || strings.HasPrefix(e.name, "/") {
		return errs.ErrInvalidInput.WithDetail(fmt.Sprintf("unsafe entry %q in archive", e.name))
	}
	target := path.Join(root, rel.String())
	if e.dir {
		return mapFSErr(r.MkdirAll(target, 0o755))
	}
	if !e.isReg {
		return nil // symlinks, devices and fifos are skipped
	}
	if err := r.MkdirAll(path.Dir(target), 0o755); err != nil {
		return mapFSErr(err)
	}
	in, err := e.open()
	if err != nil {
		return errs.ErrInvalidInput.WithDetail("cannot read archive entry")
	}
	defer in.Close()
	mode := os.FileMode(0o644)
	if e.mode&0o111 != 0 {
		mode = 0o755
	}
	out, err := r.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return mapFSErr(err)
	}
	// Cap each entry at its declared size (+1 to detect lying headers).
	n, err := io.Copy(&progressWriter{w: out, ctx: ctx, report: progressFrom(ctx)}, io.LimitReader(in, e.size+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > e.size {
		err = errs.ErrInvalidInput.WithDetail("archive entry larger than declared")
	}
	if err != nil {
		_ = r.Remove(target)
	}
	return err
}
