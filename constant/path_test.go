package constant

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPath(t *testing.T) {
	assert.False(t, (&path{}).IsSafePath("/usr/share/metacubexd/"))
	assert.True(t, (&path{
		safePaths: []string{"/usr/share/metacubexd"},
	}).IsSafePath("/usr/share/metacubexd/"))

	assert.False(t, (&path{}).IsSafePath("../metacubexd/"))
	assert.True(t, (&path{
		homeDir:   "/usr/share/mihomo",
		safePaths: []string{"/usr/share/metacubexd"},
	}).IsSafePath("../metacubexd/"))
	assert.False(t, (&path{
		homeDir:   "/usr/share/mihomo",
		safePaths: []string{"/usr/share/ycad"},
	}).IsSafePath("../metacubexd/"))

	assert.False(t, (&path{}).IsSafePath("/opt/mykeys/key1.key"))
	assert.True(t, (&path{
		safePaths: []string{"/opt/mykeys"},
	}).IsSafePath("/opt/mykeys/key1.key"))
	assert.True(t, (&path{
		safePaths: []string{"/opt/mykeys/"},
	}).IsSafePath("/opt/mykeys/key1.key"))
	assert.True(t, (&path{
		safePaths: []string{"/opt/mykeys/key1.key"},
	}).IsSafePath("/opt/mykeys/key1.key"))

	assert.True(t, (&path{}).IsSafePath("key1.key"))
	assert.True(t, (&path{}).IsSafePath("./key1.key"))
	assert.True(t, (&path{}).IsSafePath("./mykey/key1.key"))
	assert.True(t, (&path{}).IsSafePath("./mykey/../key1.key"))
	assert.False(t, (&path{}).IsSafePath("./mykey/../../key1.key"))

}

func TestFluxgateDefaultPathDoesNotAdoptMihomo(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	homeEnv := "HOME"
	if runtime.GOOS == "windows" {
		homeEnv = "USERPROFILE"
	}
	t.Setenv(homeEnv, home)
	// Empty variables are still set, so explicitly remove XDG for the first case.
	if value, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok {
		t.Cleanup(func() { _ = os.Setenv("XDG_CONFIG_HOME", value) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv("XDG_CONFIG_HOME") })
	}
	require.NoError(t, os.Unsetenv("XDG_CONFIG_HOME"))
	t.Setenv("SAFE_PATHS", "")
	t.Setenv("SKIP_SAFE_PATH_CHECK", "false")
	oldHome := filepath.Join(home, ".config", "mihomo")
	require.NoError(t, os.MkdirAll(oldHome, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(oldHome, "config.yaml"), []byte("sentinel"), 0o600))

	require.Equal(t, filepath.Join(home, ".config", "fluxgate"), filepath.FromSlash(defaultPath().HomeDir()))
	t.Setenv("XDG_CONFIG_HOME", xdg)
	require.Equal(t, filepath.Join(xdg, "fluxgate"), filepath.FromSlash(defaultPath().HomeDir()))

	newHome := filepath.Join(home, ".config", "fluxgate")
	require.NoError(t, os.MkdirAll(newHome, 0o755))
	// Preserve the existing preference for an already-created home config path.
	require.Equal(t, newHome, filepath.FromSlash(defaultPath().HomeDir()))
	oldConfig, err := os.ReadFile(filepath.Join(oldHome, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, "sentinel", string(oldConfig))
}
