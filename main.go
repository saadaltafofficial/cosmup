// cosmup installs and switches Cosmos chain binaries using the Cosmos
// chain registry, and checks a local binary against the live network.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/saadaltafofficial/cosmup/internal/installer"
	"github.com/saadaltafofficial/cosmup/internal/network"
	"github.com/saadaltafofficial/cosmup/internal/registry"
	"github.com/saadaltafofficial/cosmup/internal/store"
)

var version = "dev"

const usage = `cosmup - install and switch Cosmos chain binaries

Usage:
  cosmup install <chain>[@version] [--insecure]   install (default: registry's recommended version) and activate
  cosmup use <chain>@<version>                    activate an installed version
  cosmup list [chain]                             installed versions, or a chain's versions in the registry
  cosmup check <chain>                            compare your binary with the version the network runs
  cosmup version

<chain> is the chain's directory name in github.com/cosmos/chain-registry (e.g. zigchain, osmosis, cosmoshub).
Binaries live in $COSMUP_HOME (default ~/.cosmup); add $COSMUP_HOME/bin to the front of your PATH.
Set COSMUP_REGISTRY to use another registry base URL.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

type app struct {
	out   io.Writer
	store store.Store
	reg   *registry.Client
	http  *http.Client
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	st, err := store.Default()
	if err != nil {
		fmt.Fprintln(stderr, "cosmup:", err)
		return 1
	}
	base := os.Getenv("COSMUP_REGISTRY")
	if base == "" {
		base = registry.DefaultBaseURL
	}
	a := &app{
		out:   stdout,
		store: st,
		reg:   &registry.Client{BaseURL: base, HTTP: &http.Client{Timeout: 30 * time.Second}},
		http:  &http.Client{},
	}

	var positional []string
	insecure := false
	for _, arg := range args[1:] {
		switch {
		case arg == "--insecure":
			insecure = true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "cosmup: unknown flag %s\n", arg)
			return 1
		default:
			positional = append(positional, arg)
		}
	}

	cmd := args[0]
	switch {
	case cmd == "version":
		fmt.Fprintln(stdout, "cosmup", buildVersion())
		return 0
	case cmd == "install" && len(positional) == 1:
		err = a.install(ctx, positional[0], insecure)
	case cmd == "use" && len(positional) == 1:
		err = a.use(positional[0])
	case cmd == "list" && len(positional) <= 1:
		err = a.list(ctx, positional)
	case cmd == "check" && len(positional) == 1:
		var upToDate bool
		upToDate, err = a.check(ctx, positional[0])
		if err == nil && !upToDate {
			return 1
		}
	default:
		fmt.Fprint(stderr, usage)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "cosmup:", err)
		return 1
	}
	return 0
}

// buildVersion prefers a version set with -ldflags, then the module version
// that "go install module@version" records in the binary.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func splitSpec(spec string) (chain, ver string) {
	chain, ver, _ = strings.Cut(spec, "@")
	return chain, ver
}

func (a *app) install(ctx context.Context, spec string, insecure bool) error {
	chainName, want := splitSpec(spec)
	ch, err := a.reg.Chain(ctx, chainName)
	if err != nil {
		return err
	}
	var versions []registry.Version
	if want != "" {
		if versions, err = a.reg.Versions(ctx, chainName); err != nil {
			return err
		}
	}
	v, err := registry.Resolve(ch, versions, want)
	if err != nil {
		return err
	}

	platform := runtime.GOOS + "/" + runtime.GOARCH
	raw, ok := v.Binaries[platform]
	if !ok {
		var have []string
		for p := range v.Binaries {
			have = append(have, p)
		}
		sort.Strings(have)
		return fmt.Errorf("the registry has no %s binary for %s %s (available: %s)", platform, chainName, v.ID(), orNone(have))
	}
	art, err := registry.ParseBinary(raw)
	if err != nil {
		return err
	}
	if art.SHA256 == "" && !insecure {
		return fmt.Errorf("the registry lists no checksum for %s %s, so it can't be verified\n  rerun with --insecure to install it unverified", chainName, v.ID())
	}

	dest := a.store.BinaryPath(chainName, v.ID(), ch.DaemonName)
	if _, err := os.Stat(dest); err == nil {
		fmt.Fprintf(a.out, "%s %s is already installed\n", chainName, v.ID())
	} else {
		fmt.Fprintf(a.out, "Downloading %s %s (%s)\n", chainName, v.ID(), platform)
		if err := installer.Install(ctx, a.http, art.URL, art.SHA256, ch.DaemonName, dest); err != nil {
			return err
		}
		if art.SHA256 != "" {
			fmt.Fprintf(a.out, "Verified sha256 %s\n", art.SHA256)
		} else {
			fmt.Fprintln(a.out, "WARNING: installed without checksum verification")
		}
	}
	if err := a.store.Activate(chainName, v.ID(), ch.DaemonName); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Active: %s %s\n", chainName, v.ID())

	if out, err := localVersion(ctx, dest); err != nil {
		fmt.Fprintf(a.out, "WARNING: %s version failed: %v\n  CosmWasm chains may need libwasmvm installed separately.\n", ch.DaemonName, err)
	} else {
		fmt.Fprintf(a.out, "%s version: %s\n", ch.DaemonName, out)
	}
	a.pathHint(ch.DaemonName)
	return nil
}

func (a *app) use(spec string) error {
	chainName, want := splitSpec(spec)
	if want == "" {
		return errors.New("usage: cosmup use <chain>@<version>")
	}
	installed, err := a.store.Installed()
	if err != nil {
		return err
	}
	var have []string
	for _, in := range installed {
		if in.Chain != chainName {
			continue
		}
		have = append(have, in.Version)
		if strings.TrimPrefix(in.Version, "v") == strings.TrimPrefix(want, "v") {
			if err := a.store.Activate(chainName, in.Version, in.Daemon); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "Active: %s %s\n", chainName, in.Version)
			a.pathHint(in.Daemon)
			return nil
		}
	}
	return fmt.Errorf("%s %s is not installed (installed: %s); run: cosmup install %s", chainName, want, orNone(have), spec)
}

func (a *app) list(ctx context.Context, args []string) error {
	installed, err := a.store.Installed()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if len(installed) == 0 {
			fmt.Fprintln(a.out, "Nothing installed yet. Try: cosmup install zigchain")
			return nil
		}
		for _, in := range installed {
			fmt.Fprintf(a.out, "%s %-14s %-12s %s\n", mark(in.Active), in.Chain, in.Version, in.Daemon)
		}
		return nil
	}

	chainName := args[0]
	ch, err := a.reg.Chain(ctx, chainName)
	if err != nil {
		return err
	}
	versions, err := a.reg.Versions(ctx, chainName)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		versions = []registry.Version{ch.Codebase}
	}
	state := map[string]string{}
	for _, in := range installed {
		if in.Chain == chainName {
			state[in.Version] = "installed"
			if in.Active {
				state[in.Version] = "active"
			}
		}
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	fmt.Fprintf(a.out, "%s (%s), newest first. * = active\n", chainName, ch.DaemonName)
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		if v.ID() == "" {
			continue
		}
		var notes []string
		if v.ID() == ch.Codebase.ID() {
			notes = append(notes, "recommended")
		}
		if s := state[v.ID()]; s == "installed" {
			notes = append(notes, s)
		}
		if _, ok := v.Binaries[platform]; !ok {
			notes = append(notes, "no "+platform+" binary")
		}
		fmt.Fprintf(a.out, "%s %-12s %s\n", mark(state[v.ID()] == "active"), v.ID(), strings.Join(notes, ", "))
	}
	return nil
}

func (a *app) check(ctx context.Context, chainName string) (bool, error) {
	ch, err := a.reg.Chain(ctx, chainName)
	if err != nil {
		return false, err
	}

	bin := filepath.Join(a.store.BinDir(), ch.DaemonName)
	if _, err := os.Stat(bin); err != nil {
		bin, _ = exec.LookPath(ch.DaemonName)
	}
	local := ""
	if bin != "" {
		if local, err = localVersion(ctx, bin); err != nil {
			return false, fmt.Errorf("%s version failed: %w", bin, err)
		}
	}

	net, err := network.Query(ctx, a.http, restEndpoints(ch))
	if err != nil {
		return false, err
	}

	if bin == "" {
		fmt.Fprintf(a.out, "Local:       %s not found\n", ch.DaemonName)
	} else {
		fmt.Fprintf(a.out, "Local:       %s (%s)\n", local, bin)
	}
	fmt.Fprintf(a.out, "Network:     %s (via %s)\n", net.AppVersion, net.Endpoint)
	fmt.Fprintf(a.out, "Registry:    %s recommended\n", ch.Codebase.ID())

	if net.Plan != nil {
		fmt.Fprintf(a.out, "Upgrade:     %q scheduled at height %s\n", net.Plan.Name, net.Plan.Height)
		if versions, err := a.reg.Versions(ctx, chainName); err == nil {
			if v, err := registry.Resolve(ch, versions, net.Plan.Name); err == nil {
				fmt.Fprintf(a.out, "             prepare with: cosmup install %s@%s\n", chainName, v.ID())
			}
		}
	}

	if local != "" && strings.TrimPrefix(local, "v") == strings.TrimPrefix(net.AppVersion, "v") {
		fmt.Fprintln(a.out, "Status:      up to date")
		return true, nil
	}
	fmt.Fprintf(a.out, "Status:      does not match the network; run: cosmup install %s@%s\n", chainName, net.AppVersion)
	return false, nil
}

func restEndpoints(ch *registry.Chain) []string {
	var eps []string
	for _, e := range ch.APIs.REST {
		eps = append(eps, e.Address)
	}
	return eps
}

// localVersion runs "<bin> version". Older SDK versions print it to stderr.
func localVersion(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", errors.New("printed nothing")
}

// pathHint warns when the daemon name on PATH is not cosmup's symlink.
func (a *app) pathHint(daemon string) {
	link := filepath.Join(a.store.BinDir(), daemon)
	found, err := exec.LookPath(daemon)
	if err == nil && found == link {
		return
	}
	if err == nil {
		fmt.Fprintf(a.out, "NOTE: %s on your PATH is %s, not cosmup's.\n", daemon, found)
	}
	fmt.Fprintf(a.out, "Put cosmup first on your PATH:  export PATH=\"%s:$PATH\"\n", a.store.BinDir())
}

func mark(active bool) string {
	if active {
		return "*"
	}
	return " "
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}
