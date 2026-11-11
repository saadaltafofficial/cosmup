// Package store manages installed binaries under the cosmup home:
//
//	<root>/chains/<chain>/<version>/<daemon>   installed binaries
//	<root>/bin/<daemon>                        symlink to the active version
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Store struct{ Root string }

// Default uses $COSMUP_HOME, falling back to ~/.cosmup.
func Default() (Store, error) {
	if r := os.Getenv("COSMUP_HOME"); r != "" {
		return Store{Root: r}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Root: filepath.Join(home, ".cosmup")}, nil
}

func (s Store) BinDir() string { return filepath.Join(s.Root, "bin") }

func (s Store) BinaryPath(chain, version, daemon string) string {
	return filepath.Join(s.Root, "chains", chain, version, daemon)
}

// Activate points bin/<daemon> at the given installed version. The
// symlink is swapped with a rename, so it is never missing mid-switch.
func (s Store) Activate(chain, version, daemon string) error {
	target := s.BinaryPath(chain, version, daemon)
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("%s %s is not installed (run: cosmup install %s@%s)", chain, version, chain, version)
	}
	if err := os.MkdirAll(s.BinDir(), 0o755); err != nil {
		return err
	}
	link := filepath.Join(s.BinDir(), daemon)
	tmp := link + ".tmp"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// Installation is one installed version of a chain.
type Installation struct {
	Chain, Version, Daemon string
	Active                 bool
}

// Installed lists everything under chains/, sorted by chain then version.
func (s Store) Installed() ([]Installation, error) {
	var out []Installation
	chainsDir := filepath.Join(s.Root, "chains")
	chains, err := os.ReadDir(chainsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, c := range chains {
		versions, err := os.ReadDir(filepath.Join(chainsDir, c.Name()))
		if err != nil {
			return nil, err
		}
		for _, v := range versions {
			files, err := os.ReadDir(filepath.Join(chainsDir, c.Name(), v.Name()))
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				if f.Type().IsRegular() && !strings.HasPrefix(f.Name(), ".") {
					out = append(out, Installation{
						Chain: c.Name(), Version: v.Name(), Daemon: f.Name(),
						Active: s.isActive(c.Name(), v.Name(), f.Name()),
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Chain != out[j].Chain {
			return out[i].Chain < out[j].Chain
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func (s Store) isActive(chain, version, daemon string) bool {
	target, err := os.Readlink(filepath.Join(s.BinDir(), daemon))
	return err == nil && target == s.BinaryPath(chain, version, daemon)
}
