package installer

import (
	"archive/tar"
	"archive/zip"
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
			_, err := Install(context.Background(), http.DefaultClient, serve(t, c.body), c.sha, "zigchaind", dest)
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

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func tarGzOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// Injective-style archives ship the daemon with libwasmvm next to it.
func TestInstallBundledLibraries(t *testing.T) {
	files := map[string]string{
		"injectived":          payload,
		"peggo":               "other tool",
		"libwasmvm.x86_64.so": "lib",
		"lib/libfoo.so.1.2":   "versioned lib",
		"libwasmvm.dylib":     "mac lib",
		"README.md":           "docs",
		"../../evil.so":       "not a lib name, and must not escape",
		"../../libescape.so":  "lib name, written inside dir only",
	}
	for name, body := range map[string][]byte{"zip": zipOf(t, files), "tar.gz": tarGzOf(t, files)} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "chains", "injective", "v1.13.2", "injectived")
			libs, err := Install(context.Background(), http.DefaultClient, serve(t, body), "", "injectived", dest)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"libescape.so", "libfoo.so.1.2", "libwasmvm.dylib", "libwasmvm.x86_64.so"}
			if strings.Join(libs, ",") != strings.Join(want, ",") {
				t.Fatalf("libs = %v, want %v", libs, want)
			}
			entries, _ := os.ReadDir(filepath.Dir(dest))
			var got []string
			for _, e := range entries {
				got = append(got, e.Name())
			}
			if strings.Join(got, ",") != "injectived,"+strings.Join(want, ",") {
				t.Fatalf("version folder has %v", got)
			}
			if _, err := os.Stat(filepath.Join(root, "chains", "libescape.so")); err == nil {
				t.Fatal("archive path escaped the version folder")
			}
		})
	}
}

func TestIsSharedLib(t *testing.T) {
	for name, want := range map[string]bool{
		"libwasmvm.x86_64.so": true, "libwasmvm.aarch64.so": true, "libwasmvm.so.1": true,
		"libwasmvm.dylib": true, "injectived": false, "evil.so": false, "README.md": false, "libnotes.txt": false,
	} {
		if IsSharedLib(name) != want {
			t.Errorf("IsSharedLib(%q) = %v", name, !want)
		}
	}
}
