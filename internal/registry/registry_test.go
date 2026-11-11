package registry

import "testing"

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
