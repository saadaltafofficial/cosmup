package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	bins := map[string][]byte{"v5.1.2": fakeBinary("v5.1.2"), "v6.0.0": fakeBinary("v6.0.0"), "v4.0.0": fakeBinary("v4.0.0"), "v7.0.0": fakeBinary("v7.0.0"), "v6.1.0": fakeBinary("v6.1.0")}

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
		ver("v7", "v7.0.0", binURL("v7.0.0", true)),
		ver("v8", "v8.0.0", map[string]string{"plan9/mips": "https://x/y"}),
	}})
	writeJSON("/testnets/zigchaintestnet/chain.json", map[string]any{
		"chain_name": "zigchaintestnet", "daemon_name": "zigchaind",
		"codebase": ver("", "v6.1.0", binURL("v6.1.0", true)),
	})
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
	expect(cosmup(0, "list", "zigchain"), "v8.0.0       no "+platform+" binary", "  v7.0.0       \n", "  v6.0.0       recommended, installed", "* v5.1.2")

	expect(cosmup(1, "use", "zigchain@v4.0.0"), "not installed (installed: v5.1.2, v6.0.0)")
	expect(cosmup(1, "install", "zigchain@v8"), fmt.Sprintf("no %s binary for zigchain v8.0.0 (available: plan9/mips)", platform))
	expect(cosmup(1, "install", "zigchain@v4"), "lists no checksum", "--insecure")
	expect(cosmup(0, "install", "zigchain@v4", "--insecure"), "WARNING: installed without checksum verification", "Active: zigchain v4.0.0")
	expect(cosmup(1, "install", "nochain"), `chain "nochain" is not in the chain registry`)

	// Testnets resolve from testnets/<name>; installing one takes over zigchaind.
	expect(cosmup(0, "install", "zigchaintestnet"), "Active: zigchaintestnet v6.1.0", "zigchaind version: v6.1.0")
	expect(cosmup(0, "list", "zigchaintestnet"), "* v6.1.0       recommended")

	// update moves zigchain (which owns zigchaind again) to its recommended
	// version, but leaves the testnet inactive.
	expect(cosmup(0, "use", "zigchain@v4.0.0"), "Active: zigchain v4.0.0")
	expect(cosmup(0, "update"), "== zigchain", "Active: zigchain v6.0.0",
		"== zigchaintestnet", "Installed zigchaintestnet v6.1.0 (not activated: zigchaind belongs to another chain")
	expect(cosmup(0, "list"), "* zigchain       v6.0.0", "  zigchaintestnet v6.1.0")

	// cosmovisor: the upgrade binary goes into the node's cosmovisor folder.
	nodeHome := t.TempDir()
	t.Setenv("DAEMON_HOME", nodeHome)
	expect(cosmup(1, "cosmovisor", "zigchain"), "no cosmovisor directory at "+nodeHome)
	if err := os.MkdirAll(filepath.Join(nodeHome, "cosmovisor", "genesis", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	upgradeBin := filepath.Join(nodeHome, "cosmovisor", "upgrades", "v7", "bin", "zigchaind")
	expect(cosmup(0, "cosmovisor", "zigchain"), `Network has upgrade "v7" scheduled at height 9000000`,
		"Verified sha256", `Prepared upgrade "v7" (zigchain v7.0.0) at `+upgradeBin, "zigchaind version: v7.0.0")
	expect(cosmup(0, "cosmovisor", "zigchain", "v7"), "already prepared")
	expect(cosmup(1, "cosmovisor", "zigchain", "v9"), `upgrade "v9" of zigchain is not in the chain registry`)
	expect(cosmup(1, "cosmovisor", "zigchain", "v8"), "no "+platform+" binary")
	expect(cosmup(0, "list"), "* zigchain       v6.0.0") // cosmovisor prep doesn't change the active binary

	// remove and prune. Installed now: zigchain v4.0.0, v5.1.2, v6.0.0 (active),
	// zigchaintestnet v6.1.0 (inactive, shares zigchaind).
	expect(cosmup(1, "remove", "zigchain@v6.0.0"), "is the active version", "cosmup use zigchain@<version>")
	expect(cosmup(1, "remove", "zigchain@v9"), "zigchain v9 is not installed (installed: v4.0.0, v5.1.2, v6.0.0)")
	expect(cosmup(1, "remove", "nochain"), "nochain is not installed")
	expect(cosmup(0, "remove", "zigchain@5.1.2"), "Removed zigchain v5.1.2, ")
	expect(cosmup(0, "prune"), "Removed zigchain v4.0.0")
	out := cosmup(0, "list")
	expect(out, "* zigchain       v6.0.0", "  zigchaintestnet v6.1.0") // testnet kept: its only version
	if strings.Contains(out, "v4.0.0") || strings.Contains(out, "v5.1.2") {
		t.Fatalf("old versions still installed:\n%s", out)
	}
	expect(cosmup(0, "prune"), "Nothing to prune")
	expect(cosmup(0, "remove", "zigchaintestnet"), "Removed zigchaintestnet v6.1.0")
	expect(cosmup(0, "remove", "zigchain"), "Removed zigchain v6.0.0", "was active")
	if _, err := os.Lstat(filepath.Join(os.Getenv("COSMUP_HOME"), "bin", "zigchaind")); !os.IsNotExist(err) {
		t.Fatalf("active link still present: %v", err)
	}
	expect(cosmup(0, "list"), "Nothing installed yet")
	if _, err := os.Stat(upgradeBin); err != nil {
		t.Fatalf("remove touched the cosmovisor folder: %v", err)
	}
}

// wasmZip is an Injective-style release: the daemon plus libwasmvm. The fake
// daemon only runs when its folder is on the library path and the library
// is there, like a real dynamically linked binary.
func wasmZip(t *testing.T, ver string) []byte {
	t.Helper()
	daemon := "#!/bin/sh\n" +
		"d=$(cd \"$(dirname \"$0\")\" && pwd)\n" +
		"[ -f \"$d/libwasmvm.x86_64.so\" ] || { echo 'error while loading shared libraries: libwasmvm.x86_64.so' >&2; exit 127; }\n" +
		"case \":$LD_LIBRARY_PATH:\" in *\":$d:\"*) echo " + ver + " ;; *) echo 'error while loading shared libraries: libwasmvm.x86_64.so' >&2; exit 127 ;; esac\n"
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"wasmd": daemon, "libwasmvm.x86_64.so": "lib " + ver, "peggo": "other"} {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func TestBundledLibraries(t *testing.T) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	bins := map[string][]byte{"v1.0.0": wasmZip(t, "v1.0.0"), "v2.0.0": wasmZip(t, "v2.0.0"), "plain": []byte("#!/bin/sh\necho v3.0.0\n")}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	bin := func(k string) map[string]string {
		return map[string]string{platform: srv.URL + "/dl/" + k + "?checksum=sha256:" + sha(bins[k])}
	}
	js := func(path string, v any) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(v) })
	}
	js("/wasmchain/chain.json", map[string]any{"chain_name": "wasmchain", "daemon_name": "wasmd",
		"codebase": map[string]any{"tag": "v2.0.0", "binaries": bin("v2.0.0")}})
	js("/wasmchain/versions.json", map[string]any{"versions": []any{
		map[string]any{"name": "v1", "tag": "v1.0.0", "binaries": bin("v1.0.0")},
		map[string]any{"name": "v2", "tag": "v2.0.0", "binaries": bin("v2.0.0")},
	}})
	js("/plainchain/chain.json", map[string]any{"chain_name": "plainchain", "daemon_name": "plaind",
		"codebase": map[string]any{"tag": "v3.0.0", "binaries": bin("plain")}})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) { w.Write(bins[strings.TrimPrefix(r.URL.Path, "/dl/")]) })

	home := t.TempDir()
	t.Setenv("COSMUP_HOME", home)
	defaultURL := registryURL
	registryURL = srv.URL
	t.Cleanup(func() { registryURL = defaultURL })

	cosmup := func(wantCode int, args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		if code := run(context.Background(), args, &out, &errOut); code != wantCode {
			t.Fatalf("cosmup %v: exit %d, want %d\n%s%s", args, code, wantCode, out.String(), errOut.String())
		}
		return out.String() + errOut.String()
	}
	expect := func(out string, subs ...string) {
		t.Helper()
		for _, s := range subs {
			if !strings.Contains(out, s) {
				t.Fatalf("output missing %q:\n%s", s, out)
			}
		}
	}
	runLink := func(daemon string) string {
		t.Helper()
		out, err := exec.Command(filepath.Join(home, "bin", daemon), "version").CombinedOutput()
		if err != nil {
			t.Fatalf("%s version: %v\n%s", daemon, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	link := filepath.Join(home, "bin", "wasmd")

	// The bundled library is installed next to the binary, and bin/wasmd is
	// a wrapper that puts that folder on the library path.
	out := cosmup(0, "install", "wasmchain@v1.0.0")
	expect(out, "Bundled libraries: libwasmvm.x86_64.so", "wasmd version: v1.0.0")
	if strings.Contains(out, "WARNING") {
		t.Fatalf("unexpected warning:\n%s", out)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("bin/wasmd should be a wrapper script, not a symlink: %v", err)
	}
	if got := runLink("wasmd"); got != "v1.0.0" {
		t.Fatalf("bin/wasmd version = %q", got)
	}

	// Each version keeps its own library, and switching works both ways.
	expect(cosmup(0, "install", "wasmchain"), "Active: wasmchain v2.0.0", "wasmd version: v2.0.0")
	expect(cosmup(0, "list"), "* wasmchain      v2.0.0", "  wasmchain      v1.0.0")
	expect(cosmup(0, "use", "wasmchain@v1.0.0"), "Active: wasmchain v1.0.0")
	listOut := cosmup(0, "list")
	expect(listOut, "* wasmchain      v1.0.0       wasmd")
	if strings.Contains(listOut, "libwasmvm") {
		t.Fatalf("list shows the library as a binary:\n%s", listOut)
	}
	if got := runLink("wasmd"); got != "v1.0.0" {
		t.Fatalf("after use, bin/wasmd version = %q", got)
	}
	lib, _ := os.ReadFile(filepath.Join(home, "chains", "wasmchain", "v1.0.0", "libwasmvm.x86_64.so"))
	if string(lib) != "lib v1.0.0" {
		t.Fatalf("v1 folder has library %q", lib)
	}
	expect(cosmup(1, "remove", "wasmchain@v1.0.0"), "is the active version")

	// Chains without bundled libraries still get a plain symlink.
	expect(cosmup(0, "install", "plainchain"), "plaind version: v3.0.0")
	if st, err := os.Lstat(filepath.Join(home, "bin", "plaind")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("bin/plaind should be a symlink: %v", err)
	}

	// cosmovisor prep puts the library next to the upgrade binary and says
	// cosmovisor won't use it on its own.
	nodeHome := t.TempDir()
	t.Setenv("DAEMON_HOME", nodeHome)
	os.MkdirAll(filepath.Join(nodeHome, "cosmovisor"), 0o755)
	expect(cosmup(0, "cosmovisor", "wasmchain", "v2"), `Prepared upgrade "v2"`, "wasmd version: v2.0.0",
		"NOTE: this binary loads bundled libraries (libwasmvm.x86_64.so)", "LD_LIBRARY_PATH")
	if _, err := os.Stat(filepath.Join(nodeHome, "cosmovisor", "upgrades", "v2", "bin", "libwasmvm.x86_64.so")); err != nil {
		t.Fatalf("library not staged for cosmovisor: %v", err)
	}

	if entries, _ := os.ReadDir(filepath.Join(home, "bin")); len(entries) != 2 {
		t.Fatalf("bin/ should hold only wasmd and plaind, has %v", entries)
	}

	// Removing the chain removes the wrapper too.
	expect(cosmup(0, "remove", "wasmchain"), "Removed wasmchain v1.0.0", "was active", "Removed wasmchain v2.0.0")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("wrapper still present: %v", err)
	}
}
