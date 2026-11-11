// Package registry reads chain metadata from the Cosmos chain registry
// (github.com/cosmos/chain-registry).
package registry

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DefaultBaseURL serves raw files from the main branch of the chain registry.
const DefaultBaseURL = "https://raw.githubusercontent.com/cosmos/chain-registry/master"

// Chain is the subset of chain.json that cosmup uses.
type Chain struct {
	Name       string  `json:"chain_name"`
	ChainID    string  `json:"chain_id"`
	DaemonName string  `json:"daemon_name"`
	NodeHome   string  `json:"node_home"`
	Codebase   Version `json:"codebase"`
	APIs       struct {
		REST []Endpoint `json:"rest"`
	} `json:"apis"`

	// dir is where the chain lives in the registry: "<name>" for mainnets,
	// "testnets/<name>" for testnets.
	dir string
}

// Testnet reports whether the chain came from the registry's testnets folder.
func (c *Chain) Testnet() bool { return strings.HasPrefix(c.dir, "testnets/") }

type Endpoint struct {
	Address string `json:"address"`
}

// Version is one release of a chain, either the current codebase in
// chain.json or an entry of versions.json.
type Version struct {
	Name               string            `json:"name"`
	Tag                string            `json:"tag"`
	RecommendedVersion string            `json:"recommended_version"`
	Binaries           map[string]string `json:"binaries"`
}

// ID is the version string cosmup uses for directory names and display.
func (v Version) ID() string {
	if v.Tag != "" {
		return v.Tag
	}
	return v.RecommendedVersion
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

var errNotFound = errors.New("not found")

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Chain fetches <name>/chain.json, or testnets/<name>/chain.json if the
// name is not a mainnet.
func (c *Client) Chain(ctx context.Context, name string) (*Chain, error) {
	if name == "" || strings.ContainsAny(name, "/\\.") {
		return nil, fmt.Errorf("invalid chain name %q", name)
	}
	var ch Chain
	var err error
	for _, dir := range []string{name, "testnets/" + name} {
		ch = Chain{}
		if err = c.get(ctx, dir+"/chain.json", &ch); err == nil {
			ch.dir = dir
			break
		}
		if !errors.Is(err, errNotFound) {
			return nil, err
		}
	}
	if errors.Is(err, errNotFound) {
		return nil, fmt.Errorf("chain %q is not in the chain registry", name)
	}
	if ch.DaemonName == "" {
		return nil, fmt.Errorf("chain %q has no daemon_name in the registry", name)
	}
	return &ch, nil
}

// Versions fetches the chain's versions.json. Chains without one return nil.
func (c *Client) Versions(ctx context.Context, ch *Chain) ([]Version, error) {
	var vf struct {
		Versions []Version `json:"versions"`
	}
	if err := c.get(ctx, ch.dir+"/versions.json", &vf); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return vf.Versions, nil
}

func norm(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "v") }

// Resolve picks the version the user asked for. An empty want means the
// registry's current recommended version. want matches a tag, a
// recommended_version or an upgrade name, with or without a leading "v".
func Resolve(ch *Chain, versions []Version, want string) (Version, error) {
	if want == "" {
		if ch.Codebase.ID() == "" {
			return Version{}, fmt.Errorf("registry lists no recommended version for %s", ch.Name)
		}
		return ch.Codebase, nil
	}
	candidates := append([]Version{ch.Codebase}, reverse(versions)...)
	for _, v := range candidates {
		if v.ID() == "" {
			continue
		}
		if norm(v.Tag) == norm(want) || norm(v.RecommendedVersion) == norm(want) || (v.Name != "" && norm(v.Name) == norm(want)) {
			return v, nil
		}
	}
	return Version{}, fmt.Errorf("version %q of %s is not in the chain registry", want, ch.Name)
}

func reverse(vs []Version) []Version {
	out := make([]Version, len(vs))
	for i, v := range vs {
		out[len(vs)-1-i] = v
	}
	return out
}

// Artifact is a downloadable binary and its expected SHA256 ("" if unknown).
type Artifact struct {
	URL    string
	SHA256 string
}

// ParseBinary splits a registry binary URL into the download URL and its
// checksum. The registry uses go-getter style "?checksum=sha256:<hex>" and
// sometimes a bare "?checksum=<hex>".
func ParseBinary(raw string) (Artifact, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Artifact{}, fmt.Errorf("bad binary URL %q: %w", raw, err)
	}
	q := u.Query()
	sum := q.Get("checksum")
	q.Del("checksum")
	u.RawQuery = q.Encode()
	a := Artifact{URL: u.String()}
	if sum == "" {
		return a, nil
	}
	if typ, val, ok := strings.Cut(sum, ":"); ok {
		if typ != "sha256" {
			return Artifact{}, fmt.Errorf("unsupported checksum type %q in %s", typ, raw)
		}
		sum = val
	}
	sum = strings.ToLower(sum)
	if b, err := hex.DecodeString(sum); err != nil || len(b) != 32 {
		return Artifact{}, fmt.Errorf("checksum in %s is not a sha256 hex digest", raw)
	}
	a.SHA256 = sum
	return a, nil
}
