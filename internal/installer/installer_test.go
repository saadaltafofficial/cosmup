package installer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const payload = "#!/bin/sh\necho v6.0.0\n"

func tarGz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func serve(t *testing.T, body []byte) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	t.Cleanup(srv.Close)
	return srv.URL + "/bin"
}

func TestInstall(t *testing.T) {
	archive := tarGz(t, "bin/zigchaind", payload)
	cases := []struct {
		name    string
		body    []byte
		sha     string
		wantErr string
	}{
		{"tar.gz with checksum", archive, sum(archive), ""},
		{"raw binary", []byte(payload), sum([]byte(payload)), ""},
		{"no checksum given", []byte(payload), "", ""},
		{"checksum mismatch", archive, strings.Repeat("0", 64), "checksum mismatch"},
		{"daemon missing from archive", tarGz(t, "other", payload), "", "no file named zigchaind"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "v6.0.0", "zigchaind")
			err := Install(context.Background(), http.DefaultClient, serve(t, c.body), c.sha, "zigchaind", dest)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				if _, statErr := os.Stat(dest); statErr == nil {
					t.Fatal("binary written despite error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(dest)
			st, _ := os.Stat(dest)
			if string(got) != payload || st.Mode().Perm() != 0o755 {
				t.Fatalf("got %q mode %v", got, st.Mode())
			}
		})
	}
}
