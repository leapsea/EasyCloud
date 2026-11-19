package plugin

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/easycloud/easycloud/internal/pathguard"
)

const (
	manifestName  = "manifest.json"
	signatureName = "signature.json"

	maxPackageSize   = 512 << 20
	maxExtractedSize = 1 << 30
	maxFiles         = 5000
)

// Signature is stored as signature.json inside the package.
type Signature struct {
	KeyID string `json:"keyId"` // hex sha256 of the public key, first 16 chars
	Sig   string `json:"sig"`   // base64 ed25519 signature over Digest()
}

// Package is a parsed, not yet installed plugin archive.
type Package struct {
	Manifest  *Manifest
	Signature *Signature
	digest    []byte
	zr        *zip.Reader
}

func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:])[:16]
}

// digest hashes every file except the signature: "path\x00sha256\n", sorted by path.
func digest(zr *zip.Reader) ([]byte, error) {
	var lines []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.Name == signatureName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, io.LimitReader(rc, maxExtractedSize))
		rc.Close()
		if err != nil {
			return nil, err
		}
		lines = append(lines, f.Name+"\x00"+hex.EncodeToString(h.Sum(nil))+"\n")
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "")))
	return h[:], nil
}

// Open parses and validates archive structure (not the signature).
func Open(data []byte) (*Package, error) {
	if len(data) > maxPackageSize {
		return nil, fmt.Errorf("package larger than %d MB", maxPackageSize>>20)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a zip archive: %w", err)
	}
	if len(zr.File) > maxFiles {
		return nil, fmt.Errorf("too many files")
	}
	var total uint64
	p := &Package{zr: zr}
	for _, f := range zr.File {
		// Zip Slip: every entry must be a clean relative path.
		name := strings.TrimSuffix(f.Name, "/")
		if rp, err := pathguard.Parse(name); err != nil || rp.String() != name || strings.HasPrefix(f.Name, "/") {
			return nil, fmt.Errorf("unsafe path in archive: %q", f.Name)
		}
		if m := f.Mode(); m&os.ModeSymlink != 0 || (!m.IsRegular() && !m.IsDir()) {
			return nil, fmt.Errorf("archive entry %q is not a regular file", f.Name)
		}
		total += f.UncompressedSize64
		if total > maxExtractedSize {
			return nil, fmt.Errorf("archive expands beyond %d MB", maxExtractedSize>>20)
		}
		switch f.Name {
		case manifestName:
			b, err := readZip(f, 1<<20)
			if err != nil {
				return nil, err
			}
			if p.Manifest, err = ParseManifest(b); err != nil {
				return nil, err
			}
		case signatureName:
			b, err := readZip(f, 1<<16)
			if err != nil {
				return nil, err
			}
			var s Signature
			if err := json.Unmarshal(b, &s); err != nil {
				return nil, fmt.Errorf("signature.json: %w", err)
			}
			p.Signature = &s
		}
	}
	if p.Manifest == nil {
		return nil, errors.New("manifest.json missing at archive root")
	}
	if p.digest, err = digest(zr); err != nil {
		return nil, err
	}
	return p, nil
}

func readZip(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

// Verify checks the signature against trusted keys. It returns the key id used.
func (p *Package) Verify(trusted []ed25519.PublicKey) (string, error) {
	if p.Signature == nil {
		return "", errors.New("package is not signed")
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature.Sig)
	if err != nil {
		return "", errors.New("signature is not base64")
	}
	for _, k := range trusted {
		if KeyID(k) == p.Signature.KeyID && ed25519.Verify(k, p.digest, sig) {
			return p.Signature.KeyID, nil
		}
	}
	return "", fmt.Errorf("signature by key %s does not verify against any trusted key", p.Signature.KeyID)
}

// Extract writes the archive into dir (which must not exist yet).
func (p *Package) Extract(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, f := range p.zr.File {
		if f.Name == signatureName {
			continue
		}
		name := strings.TrimSuffix(f.Name, "/")
		if f.FileInfo().IsDir() {
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
			continue
		}
		if d := filepath.Dir(name); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
		mode := os.FileMode(0o644)
		if f.Mode()&0o111 != 0 {
			mode = 0o755
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(rc, maxExtractedSize))
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Build creates a package from a directory, signing it when key is non-nil.
// Used by the ecplugin tool and tests.
func Build(srcDir string, key ed25519.PrivateKey) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." || d.IsDir() || rel == signatureName || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name, hdr.Method = rel, zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if key == nil {
		return buf.Bytes(), nil
	}
	pkg, err := Open(buf.Bytes())
	if err != nil {
		return nil, err
	}
	sig := Signature{KeyID: KeyID(key.Public().(ed25519.PublicKey)), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, pkg.digest))}
	// Re-write the archive with signature.json appended.
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	var out bytes.Buffer
	zw = zip.NewWriter(&out)
	for _, f := range zr.File {
		if err := zw.Copy(f); err != nil {
			return nil, err
		}
	}
	w, err := zw.Create(signatureName)
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(w).Encode(sig); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
