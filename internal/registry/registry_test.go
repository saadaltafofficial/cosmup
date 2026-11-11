package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseBinary(t *testing.T) {
	const h = "4373e53d7a05ff0f50037a1cd83801d5f0d972207e7ff50f3cc5d8ded62867a4"
	cases := []struct {
		name, in, url, sum string
		wantErr            bool
	}{
		{"zigchain prefixed", "https://x/z.tar.gz?checksum=sha256:" + h, "https://x/z.tar.gz", h, false},
		{"osmosis bare", "https://x/osmosisd?checksum=" + h, "https://x/osmosisd", h, false},
		{"gaia none", "https://x/gaiad", "https://x/gaiad", "", false},
		{"other params kept", "https://x/b?a=1&checksum=sha256:" + h, "https://x/b?a=1", h, false},
		{"sha512 rejected", "https://x/b?checksum=sha512:abcd", "", "", true},
		{"short hex rejected", "https://x/b?checksum=abcd", "", "", true},
	}
	for _, c := range cases {
		a, err := ParseBinary(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
		if err == nil && (a.URL != c.url || a.SHA256 != c.sum) {
			t.Errorf("%s: got %+v", c.name, a)
		}
	}
}

func TestResolve(t *testing.T) {
	ch := &Chain{Name: "zigchain", Codebase: Version{Tag: "v6.0.0", RecommendedVersion: "6.0.0"}}
	vs := []Version{
		{Name: "v5", Tag: "v5.1.2", RecommendedVersion: "5.1.2"},
		{Name: "v6", Tag: "v6.0.0", RecommendedVersion: "6.0.0"},
	}
	for want, id := range map[string]string{"": "v6.0.0", "5.1.2": "v5.1.2", "v5.1.2": "v5.1.2", "v5": "v5.1.2", "6.0.0": "v6.0.0"} {
		v, err := Resolve(ch, vs, want)
		if err != nil || v.ID() != id {
			t.Errorf("Resolve(%q) = %q, %v; want %q", want, v.ID(), err, id)
		}
	}
	if _, err := Resolve(ch, vs, "v9.9.9"); err == nil {
		t.Error("expected error for unknown version")
	}
}

func TestChainTestnetFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/osmosis/chain.json":
			w.Write([]byte(`{"chain_name":"osmosis","daemon_name":"osmosisd"}`))
		case "/testnets/osmosistestnet/chain.json":
			w.Write([]byte(`{"chain_name":"osmosistestnet","daemon_name":"osmosisd"}`))
		case "/testnets/osmosistestnet/versions.json":
			w.Write([]byte(`{"versions":[{"name":"v29","tag":"v29.0.0"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	ctx := context.Background()

	main, err := c.Chain(ctx, "osmosis")
	if err != nil || main.Testnet() {
		t.Fatalf("osmosis: %v testnet=%v", err, main != nil && main.Testnet())
	}
	tn, err := c.Chain(ctx, "osmosistestnet")
	if err != nil || !tn.Testnet() {
		t.Fatalf("osmosistestnet: %v", err)
	}
	vs, err := c.Versions(ctx, tn)
	if err != nil || len(vs) != 1 || vs[0].Tag != "v29.0.0" {
		t.Fatalf("testnet versions = %v, %v", vs, err)
	}
	for _, bad := range []string{"nochain", "../etc", "a/b", ""} {
		if _, err := c.Chain(ctx, bad); err == nil {
			t.Errorf("Chain(%q) succeeded", bad)
		}
	}
}
