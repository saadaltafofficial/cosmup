// Package installer downloads a chain binary, verifies its checksum and
// extracts the daemon from it.
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
)

// Install downloads url, checks it against sha256 (unless it is empty) and
// writes the daemon binary to dest with mode 0755. Archives (tar.gz, zip)
// are searched for a file named daemon; anything else is taken as the raw
// binary.
func Install(ctx context.Context, client *http.Client, url, sha256Hex, daemon, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	dl, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(dl.Name())
	defer dl.Close()

	sum, err := download(ctx, client, url, dl)
	if err != nil {
		return err
	}
	if sha256Hex != "" && sum != sha256Hex {
		return fmt.Errorf("checksum mismatch for %s:\n  expected %s\n  got      %s", url, sha256Hex, sum)
	}
	if _, err := dl.Seek(0, io.SeekStart); err != nil {
		return err
	}

	out, err := os.CreateTemp(filepath.Dir(dest), ".bin-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	if err := extract(dl, daemon, out); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(out.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(out.Name(), dest)
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

// extract picks the format from the file's first bytes, since registry
// URLs do not reliably carry an extension.
func extract(f *os.File, daemon string, out io.Writer) error {
	magic, err := bufio.NewReader(f).Peek(4)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch {
	case len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		return fromTarGz(f, daemon, out)
	case len(magic) >= 4 && string(magic) == "PK\x03\x04":
		return fromZip(f, daemon, out)
	default:
		_, err := io.Copy(out, f)
		return err
	}
}

func fromTarGz(r io.Reader, daemon string, out io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("archive has no file named %s", daemon)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == daemon {
			_, err := io.Copy(out, tr)
			return err
		}
	}
}

func fromZip(f *os.File, daemon string, out io.Writer) error {
	st, err := f.Stat()
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return err
	}
	for _, zf := range zr.File {
		if !zf.FileInfo().IsDir() && path.Base(zf.Name) == daemon {
			rc, err := zf.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("archive has no file named %s", daemon)
}
