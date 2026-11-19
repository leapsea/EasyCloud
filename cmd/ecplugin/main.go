// Command ecplugin builds, signs and indexes EasyCloud plugin packages.
//
//	ecplugin keygen -out dev                 # writes dev.key (private) and dev.pub
//	ecplugin pack -dir plugins/x -key dev.key -out x-1.0.0.zip
//	ecplugin verify -pub dev.pub x-1.0.0.zip
//	ecplugin entry -url https://host/x-1.0.0.zip x-1.0.0.zip > x-1.0.0.json
//	ecplugin index entries/*.json > index.json          # repository index from entries
//	ecplugin index -base https://example.com/plugins/ *.zip > index.json   # or straight from packages
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/easycloud/easycloud/internal/plugin"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "pack":
		err = pack(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "entry":
		err = entry(os.Args[2:])
	case "index":
		err = index(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ecplugin keygen|pack|verify|entry|index [flags]")
	os.Exit(2)
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "plugin-signing", "output file prefix")
	fs.Parse(args)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out+".key", []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(*out+".pub", []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("key id %s\npublic key (add to EASYCLOUD_PLUGINS_TRUSTED_KEYS): %s\n", plugin.KeyID(pub), base64.StdEncoding.EncodeToString(pub))
	return nil
}

func readKey(path string, size int) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(k) != size {
		return nil, fmt.Errorf("%s is not a base64 ed25519 key", path)
	}
	return k, nil
}

func pack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ExitOnError)
	dir := fs.String("dir", ".", "plugin directory containing manifest.json")
	keyPath := fs.String("key", "", "private key (omit for an unsigned package)")
	out := fs.String("out", "", "output .zip (default <id>-<version>.zip)")
	fs.Parse(args)
	var key ed25519.PrivateKey
	if *keyPath != "" {
		k, err := readKey(*keyPath, ed25519.PrivateKeySize)
		if err != nil {
			return err
		}
		key = k
	}
	data, err := plugin.Build(*dir, key)
	if err != nil {
		return err
	}
	pkg, err := plugin.Open(data)
	if err != nil {
		return err
	}
	if err := pkg.Manifest.Validate("dev"); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if *out == "" {
		*out = pkg.Manifest.ID + "-" + pkg.Manifest.Version + ".zip"
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	signed := "unsigned"
	if key != nil {
		signed = "signed by " + plugin.KeyID(key.Public().(ed25519.PublicKey))
	}
	fmt.Printf("%s (%d bytes, %s)\n", *out, len(data), signed)
	return nil
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubPath := fs.String("pub", "", "public key file")
	fs.Parse(args)
	pub, err := readKey(*pubPath, ed25519.PublicKeySize)
	if err != nil {
		return err
	}
	for _, f := range fs.Args() {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		pkg, err := plugin.Open(data)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		if _, err := pkg.Verify([]ed25519.PublicKey{pub}); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		fmt.Printf("%s: OK (%s %s)\n", f, pkg.Manifest.ID, pkg.Manifest.Version)
	}
	return nil
}

// entry prints the repository entry for one package published at -url.
func entry(args []string) error {
	fs := flag.NewFlagSet("entry", flag.ExitOnError)
	url := fs.String("url", "", "download URL of the published package")
	fs.Parse(args)
	if *url == "" || fs.NArg() != 1 {
		return fmt.Errorf("usage: ecplugin entry -url URL package.zip")
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	e, err := plugin.EntryFor(data, *url)
	if err != nil {
		return err
	}
	return printJSON(e)
}

// index builds the repository index from entry files (.json) and/or packages
// (.zip, published under -base).
func index(args []string) error {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	base := fs.String("base", "", "URL prefix where .zip arguments are published")
	fs.Parse(args)
	var entries []plugin.RepoEntry
	for _, f := range fs.Args() {
		data, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var e plugin.RepoEntry
		if strings.HasSuffix(f, ".json") {
			err = json.Unmarshal(data, &e)
		} else {
			e, err = plugin.EntryFor(data, *base+filepath.Base(f))
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		entries = append(entries, e)
	}
	idx, err := plugin.BuildIndex(entries)
	if err != nil {
		return err
	}
	return printJSON(idx)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false) // keep ">=0.3.0" readable
	return enc.Encode(v)
}
