package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bontaramsonta/poof/exit"
)

// configPath is ~/.config/poof/config on every platform, as the Static
// IP guide documents; os.UserConfigDir would pick ~/Library on macOS.
func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "poof", "config"), nil
}

// parseConfig reads `key = value` lines; blank lines and # comments are
// skipped. Unknown keys are errors, so a typo can't silently unset the
// owner.
func parseConfig(r io.Reader) (map[string]string, error) {
	cfg := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: want key = value", n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "owner" {
			return nil, fmt.Errorf("line %d: unknown key %q", n, k)
		}
		cfg[k] = v
	}
	return cfg, sc.Err()
}

// resolveOwner returns --owner if given, else the config's owner. With
// neither it refuses: acting on someone else's Static IP is the
// accident Owners exist to prevent.
func resolveOwner(flag, path string) (string, error) {
	owner := flag
	if owner == "" {
		f, err := os.Open(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return "", err
		default:
			defer f.Close()
			cfg, err := parseConfig(f)
			if err != nil {
				return "", fmt.Errorf("%s: %w", path, err)
			}
			owner = cfg["owner"]
		}
	}
	if owner == "" {
		return "", fmt.Errorf("no owner set: add `owner = <name>` to %s, or pass --owner", path)
	}
	if err := exit.ValidOwner(owner); err != nil {
		return "", err
	}
	return owner, nil
}
