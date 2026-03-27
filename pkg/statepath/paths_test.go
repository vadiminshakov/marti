package statepath

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWALDirUsesStateDirOverride(t *testing.T) {
	t.Setenv(EnvVar, t.TempDir())

	dir, err := WALDir("bot_a")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(os.Getenv(EnvVar), "wal", "bot_a"), dir)
}

func TestExpandUserExpandsHomePrefix(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	expanded, err := ExpandUser("~/.marti")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(homeDir, ".marti"), expanded)
}
