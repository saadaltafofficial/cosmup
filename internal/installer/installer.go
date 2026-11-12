// Package installer downloads a chain binary, verifies its checksum and
// extracts the daemon (and any shared libraries bundled with it).
package installer

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// IsSharedLib reports whether a file name looks like a shared library that
// a chain binary may load at startup, such as libwasmvm.x86_64.so or
// libwasmvm.dylib.
func IsSharedLib(name string) bool {
	return strings.HasPrefix(name, "lib") && (strings.HasSuffix(name, ".so") || strings.Contains(name, ".so.")) ||
		strings.HasSuffix(name, ".dylib")
}

// Install downloads url, checks it against sha256 (unless it is empty) and
// writes the daemon binary to dest with mode 0755. Archives (tar.gz, zip)
// are searched for a file named daemon; anything else is taken as the raw
// binary. Shared libraries in an archive are written next to dest, and
// their names are returned. dest is written last, so it only exists once
// everything else is in place.
func Install(ctx context.Context, client *http.Client, url, sha256Hex, daemon, dest string) ([]string, error) {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dl, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(dl.Name())
	defer dl.Close()

	sum, err := download(ctx, client, url, dl)
	if err != nil {
		return nil, err
	}
	if sha256Hex != "" && sum != sha256Hex {
		return nil, fmt.Errorf("checksum mismatch for %s:\n  expected %s\n  got      %s", url, sha256Hex, sum)
	}
	if _, err := dl.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp(dir, ".extract-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	if err := extract(dl, daemon, staging); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return nil, err
	}
	var libs []string
	for _, e := range entries {
		if e.Name() != daemon {
			if err := os.Rename(filepath.Join(staging, e.Name()), filepath.Join(dir, e.Name())); err != nil {
				return nil, err
			}
			libs = append(libs, e.Name())
		}
	}
	sort.Strings(libs)
	if err := os.Chmod(filepath.Join(staging, daemon), 0o755); err != nil {
		return nil, err
	}
	return libs, os.Rename(filepath.Join(staging, daemon), dest)
}

func download(ctx context.Context, client *http.Client, url string, w io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extract writes the daemon, and any shared libraries from an archive, into
// dir. Files keep only their base names, so archive paths can't escape dir.
// The format is picked from the file's first bytes, since registry URLs do
// not reliably carry an extension.
func extract(f *os.File, daemon, dir string) error {
	magic, err := bufio.NewReader(f).Peek(4)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch {
	case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		return fromTarGz(f, daemon, dir)
	case len(magic) >= 4 && string(magic) == "PK\x03\x04":
		return fromZip(f, daemon, dir)
	default:
		return writeFile(filepath.Join(dir, daemon), f)
	}
}

func wanted(name, daemon string) bool {
	return name == daemon || IsSharedLib(name)
}

func fromTarGz(r io.Reader, daemon, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := path.Base(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || !wanted(name, daemon) {
			continue
		}
		if err := writeFile(filepath.Join(dir, name), tr); err != nil {
			return err
		}
		found = found || name == daemon
	}
	if !found {
		return fmt.Errorf("archive has no file named %s", daemon)
	}
	return nil
}

func fromZip(f *os.File, daemon, dir string) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return err
	}
	found := false
	for _, zf := range zr.File {
		name := path.Base(zf.Name)
		if zf.FileInfo().IsDir() || !zf.Mode().IsRegular() || !wanted(name, daemon) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(filepath.Join(dir, name), rc)
		rc.Close()
		if err != nil {
			return err
		}
		found = found || name == daemon
	}
	if !found {
		return fmt.Errorf("archive has no file named %s", daemon)
	}
	return nil
}

func writeFile(p string, r io.Reader) error {
	out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
