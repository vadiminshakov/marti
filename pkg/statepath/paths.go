package statepath

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
)

const (
	// EnvVar overrides the default base directory for Marti state files.
	EnvVar = "MARTI_STATE_DIR"

	defaultDirName  = ".marti"
	walDirName      = "wal"
	simulateDirName = "simulate"
)

// RootDir returns the base directory for Marti state files.
func RootDir() (string, error) {
	if stateDir := strings.TrimSpace(os.Getenv(EnvVar)); stateDir != "" {
		resolved, err := ExpandUser(stateDir)
		if err != nil {
			return "", errors.Wrap(err, "resolve MARTI_STATE_DIR")
		}

		return resolved, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "resolve user home directory")
	}

	return filepath.Join(homeDir, defaultDirName), nil
}

// WALDir returns a path inside the shared WAL directory.
func WALDir(parts ...string) (string, error) {
	rootDir, err := RootDir()
	if err != nil {
		return "", err
	}

	elements := append([]string{rootDir, walDirName}, parts...)

	return filepath.Join(elements...), nil
}

// SimulateDir returns the default directory for simulator state files.
func SimulateDir() (string, error) {
	rootDir, err := RootDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(rootDir, simulateDirName), nil
}

// ExpandUser expands a leading tilde in a path to the current user's home directory.
func ExpandUser(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "resolve user home directory")
	}

	if path == "~" {
		return homeDir, nil
	}

	return filepath.Join(homeDir, path[2:]), nil
}
