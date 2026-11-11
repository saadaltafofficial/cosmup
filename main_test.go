package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// fakeBinary is a shell script standing in for zigchaind.
func fakeBinary(ver string) []byte {
	body := "#!/bin/sh\necho " + ver + "\n"
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "zigchaind", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func TestEndToEnd(t *testing.T) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	bins := map[string][]byte{"v5.1.2": fakeBinary("v5.1.2"), "v6.0.0": fakeBinary("v6.0.0"), "v4.0.0": fakeBinary("v4.0.0")}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	binURL := func(v string, checksum bool) map[string]string {
		u := srv.URL + "/dl/" + v
		if checksum {
			u += "?checksum=sha256:" + sha(bins[v])
		}
		return map[string]string{platform: u}
	}
	ver := func(name, tag string, bin map[string]string) map[string]any {
		return map[string]any{"name": name, "tag": tag, "recommended_version": strings.TrimPrefix(tag, "v"), "binaries": bin}
	}
	writeJSON := func(path string, v any) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(v) })
	}
	writeJSON("/zigchain/chain.json", map[string]any{
		"chain_name": "zigchain", "daemon_name": "zigchaind",
		"codebase": ver("", "v6.0.0", binURL("v6.0.0", true)),
		"apis":     map[string]any{"rest": []map[string]string{{"address": "http://127.0.0.1:1/dead"}, {"address": srv.URL + "/lcd"}}},
	})
	writeJSON("/zigchain/versions.json", map[string]any{"versions": []any{
		ver("v4", "v4.0.0", binURL("v4.0.0", false)),
		ver("v5", "v5.1.2", binURL("v5.1.2", true)),
		ver("v6", "v6.0.0", binURL("v6.0.0", true)),
		ver("v7", "v7.0.0", map[string]string{"plan9/mips": "https://x/y"}),
	}})
	writeJSON("/lcd/cosmos/base/tendermint/v1beta1/node_info", map[string]any{"application_version": map[string]string{"version": "v6.0.0"}})
	writeJSON("/lcd/cosmos/upgrade/v1beta1/current_plan", map[string]any{"plan": map[string]string{"name": "v7", "height": "9000000"}})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) { w.Write(bins[strings.TrimPrefix(r.URL.Path, "/dl/")]) })

	t.Setenv("COSMUP_HOME", t.TempDir())
	defaultURL := registryURL
	registryURL = srv.URL
	t.Cleanup(func() { registryURL = defaultURL })

	cosmup := func(wantCode int, args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		code := run(context.Background(), args, &out, &errOut)
		all := out.String() + errOut.String()
		if code != wantCode {
			t.Fatalf("cosmup %v: exit %d, want %d\n%s", args, code, wantCode, all)
		}
		return all
	}
	expect := func(out string, subs ...string) {
		t.Helper()
		for _, s := range subs {
			if !strings.Contains(out, s) {
				t.Fatalf("output missing %q:\n%s", s, out)
			}
		}
	}

	expect(cosmup(0, "install", "zigchain@5.1.2"), "Verified sha256", "Active: zigchain v5.1.2", "zigchaind version: v5.1.2")
	expect(cosmup(1, "check", "zigchain"),
		"Local:       v5.1.2", "Network:     v6.0.0", `"v7" scheduled at height 9000000`,
		"prepare with: cosmup install zigchain@v7.0.0", "does not match the network; run: cosmup install zigchain@v6.0.0")

	expect(cosmup(0, "install", "zigchain"), "Active: zigchain v6.0.0", "zigchaind version: v6.0.0")
	expect(cosmup(0, "check", "zigchain"), "Status:      up to date")
	expect(cosmup(0, "install", "zigchain"), "already installed")

	expect(cosmup(0, "use", "zigchain@5.1.2"), "Active: zigchain v5.1.2")
	expect(cosmup(0, "list"), "* zigchain       v5.1.2", "  zigchain       v6.0.0")
	expect(cosmup(0, "list", "zigchain"), "v7.0.0       no "+platform+" binary", "  v6.0.0       recommended, installed", "* v5.1.2")

	expect(cosmup(1, "use", "zigchain@v4.0.0"), "not installed (installed: v5.1.2, v6.0.0)")
	expect(cosmup(1, "install", "zigchain@v7"), fmt.Sprintf("no %s binary for zigchain v7.0.0 (available: plan9/mips)", platform))
	expect(cosmup(1, "install", "zigchain@v4"), "lists no checksum", "--insecure")
	expect(cosmup(0, "install", "zigchain@v4", "--insecure"), "WARNING: installed without checksum verification", "Active: zigchain v4.0.0")
	expect(cosmup(1, "install", "nochain"), `chain "nochain" is not in the chain registry`)
}
