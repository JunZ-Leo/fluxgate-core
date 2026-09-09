package updater

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	C "github.com/metacubex/mihomo/constant"
	"github.com/stretchr/testify/require"
)

func TestCoreBaseName(t *testing.T) {
	for _, tc := range []struct {
		os, arch, arm, mips, amd, want string
	}{
		{"linux", "amd64", "", "", "v1", "fluxgate-linux-amd64-v1"},
		{"windows", "amd64", "", "", "v3", "fluxgate-windows-amd64-v3"},
		{"linux", "arm", "7", "", "", "fluxgate-linux-armv7"},
		{"linux", "arm", "7,hardfloat", "", "", "fluxgate-linux-armv7"},
		{"android", "arm64", "", "", "", "fluxgate-android-arm64-v8"},
		{"linux", "arm64", "", "", "", "fluxgate-linux-arm64"},
		{"darwin", "arm64", "", "", "", "fluxgate-darwin-arm64"},
		{"linux", "mipsle", "", "softfloat", "", "fluxgate-linux-mipsle-softfloat"},
		{"linux", "mips", "", "hardfloat", "", "fluxgate-linux-mips-hardfloat"},
		{"linux", "riscv64", "", "", "", "fluxgate-linux-riscv64"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			require.Equal(t, tc.want, coreBaseName(tc.os, tc.arch, tc.arm, tc.mips, tc.amd))
		})
	}
	require.True(t, strings.HasPrefix(DefaultCoreUpdater.CoreBaseName(), "fluxgate-"))
}

func TestStableReleaseSource(t *testing.T) {
	for _, channel := range []string{"", "auto", "release", "RELEASE"} {
		source, err := releaseSource(channel)
		require.NoError(t, err)
		require.Equal(t, "https://github.com/JunZ-Leo/fluxgate-core/releases/", source)
	}
	for _, channel := range []string{"alpha", "ALPHA", "beta", "other", "https://github.com/MetaCubeX/mihomo"} {
		// An invalid channel fails before any filesystem access or network request.
		err := DefaultCoreUpdater.Update(filepath.Join(t.TempDir(), "missing"), channel, true)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "currentExePath")
		if strings.EqualFold(channel, "alpha") {
			require.Contains(t, err.Error(), "stable releases only")
		}
	}
}

func TestLatestVersionValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		valid            bool
	}{
		{"stable", "v0.1.0\n", "v0.1.0", 200, true},
		{"CRLF", "v10.2.3\r\n", "v10.2.3", 200, true},
		{"no release", "Not Found", "", 404, false},
		{"error body resembling version", "v0.1.0", "", 500, false},
		{"prerelease", "v0.1.0-rc.1", "", 200, false},
		{"traversal", "../../other", "", 200, false},
		{"leading zero", "v01.2.3", "", 200, false},
		{"wrong content", "<html>error</html>", "", 200, false},
		{"oversize", strings.Repeat("a", 129), "", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			version, err := DefaultCoreUpdater.getLatestVersion(server.URL)
			if tc.valid {
				require.NoError(t, err)
				require.Equal(t, tc.want, version)
			} else {
				require.Error(t, err)
				require.Empty(t, version)
			}
		})
	}
}

func TestChecksumManifest(t *testing.T) {
	hash := sha256.Sum256([]byte("binary"))
	hexHash := hex.EncodeToString(hash[:])
	name := "fluxgate-linux-amd64-v1-v0.1.0.gz"
	for _, entry := range []string{name, "./" + name, "*" + name, "*./" + name} {
		value, err := checksumFor([]byte(hexHash+"  "+entry+"\n"), name)
		require.NoError(t, err)
		require.Equal(t, hash[:], value)
	}
	for _, manifest := range []string{
		"", hexHash + " other.gz", "invalid " + name, "ab " + name,
		hexHash + " " + name + "\n" + hexHash + " ./" + name,
	} {
		_, err := checksumFor([]byte(manifest), name)
		require.Error(t, err)
	}
}

func TestCopyBounded(t *testing.T) {
	for _, size := range []int{7, 8, 9} {
		var dst bytes.Buffer
		err := copyBounded(&dst, bytes.NewReader(bytes.Repeat([]byte{'x'}, size)), 8)
		if size > 8 {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Len(t, dst.Bytes(), size)
		}
	}
}

func coreArchive(t *testing.T, name string, content []byte, zipFormat bool) []byte {
	t.Helper()
	var data bytes.Buffer
	if zipFormat {
		w := zip.NewWriter(&data)
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write(content)
		require.NoError(t, err)
		require.NoError(t, w.Close())
	} else {
		w := gzip.NewWriter(&data)
		w.Name = name
		_, err := w.Write(content)
		require.NoError(t, err)
		require.NoError(t, w.Close())
	}
	return data.Bytes()
}

func TestUnpackCoreIdentity(t *testing.T) {
	for _, zipFormat := range []bool{false, true} {
		for _, valid := range []bool{false, true} {
			t.Run(fmt.Sprintf("zip=%t/valid=%t", zipFormat, valid), func(t *testing.T) {
				dir := t.TempDir()
				expected, ext := "fluxgate-linux-amd64-v1", ".gz"
				if zipFormat {
					expected, ext = "fluxgate-windows-amd64-v1.exe", ".zip"
				}
				name := expected
				if !valid {
					name = "../outside"
				}
				path := filepath.Join(dir, "archive"+ext)
				require.NoError(t, os.WriteFile(path, coreArchive(t, name, []byte("binary"), zipFormat), 0o600))
				output, err := unpackCore(path, dir, expected, 0o755)
				if !valid {
					require.Error(t, err)
					require.Empty(t, output)
					_, err = os.Stat(filepath.Join(dir, expected))
					require.True(t, os.IsNotExist(err))
				} else {
					require.NoError(t, err)
					require.Equal(t, filepath.Join(dir, expected), output)
					content, err := os.ReadFile(output)
					require.NoError(t, err)
					require.Equal(t, "binary", string(content))
				}
			})
		}
	}
}

func TestUpdatePinnedRelease(t *testing.T) {
	for _, failure := range []string{"", "version", "manifest", "checksum", "archive", "truncated archive", "extra member", "wrong member", "backup"} {
		t.Run(failure, func(t *testing.T) {
			if failure == "extra member" && runtime.GOOS == "windows" {
				t.Skip("gzip is used for non-Windows updates")
			}
			dir := t.TempDir()
			current := filepath.Join(dir, "fluxgate")
			require.NoError(t, os.WriteFile(current, []byte("old-core"), 0o755))
			mihomo := filepath.Join(dir, "mihomo")
			require.NoError(t, os.WriteFile(mihomo, []byte("unrelated-core"), 0o755))
			exeName, ext := DefaultCoreUpdater.CoreBaseName(), ".gz"
			if runtime.GOOS == "windows" {
				exeName += ".exe"
				ext = ".zip"
			}
			member := exeName
			if failure == "wrong member" {
				member = "mihomo"
			}
			archive := coreArchive(t, member, []byte("new-core"), runtime.GOOS == "windows")
			if failure == "truncated archive" {
				archive = archive[:len(archive)/2]
			}
			if failure == "extra member" {
				archive = append(archive, coreArchive(t, "other-core", []byte("extra"), false)...)
			}
			hash := sha256.Sum256(archive)
			if failure == "checksum" {
				hash = sha256.Sum256([]byte("other"))
			}
			version := "v0.1.0"
			packageName := DefaultCoreUpdater.CoreBaseName() + "-" + version + ext
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				switch r.URL.Path {
				case "/latest/download/version.txt":
					if failure == "version" {
						w.WriteHeader(404)
						return
					}
					_, _ = io.WriteString(w, version+"\n")
				case "/download/" + version + "/checksums.txt":
					if failure == "manifest" {
						w.WriteHeader(404)
						return
					}
					_, _ = fmt.Fprintf(w, "%x  ./%s\n", hash, packageName)
				case "/download/" + version + "/" + packageName:
					if failure == "archive" {
						w.WriteHeader(403)
						return
					}
					_, _ = w.Write(archive)
				default:
					t.Errorf("unexpected, unpinned request: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			if failure == "backup" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "fluxgate-backup"), []byte("occupied"), 0o600))
			}
			err := DefaultCoreUpdater.update(current, server.URL+"/", false)
			content, readErr := os.ReadFile(current)
			require.NoError(t, readErr)
			if failure != "" {
				require.Error(t, err)
				require.Equal(t, "old-core", string(content))
			} else {
				require.NoError(t, err)
				require.Equal(t, "new-core", string(content))
				backup, err := os.ReadFile(filepath.Join(dir, "fluxgate-backup", "fluxgate"))
				require.NoError(t, err)
				require.Equal(t, "old-core", string(backup))
				mu.Lock()
				observed := append([]string(nil), paths...)
				mu.Unlock()
				require.Equal(t, []string{
					"/latest/download/version.txt", "/download/" + version + "/checksums.txt",
					"/download/" + version + "/" + packageName,
				}, observed)
			}
			unrelated, err := os.ReadFile(mihomo)
			require.NoError(t, err)
			require.Equal(t, "unrelated-core", string(unrelated))
			temp, err := filepath.Glob(filepath.Join(dir, ".fluxgate-update-*"))
			require.NoError(t, err)
			require.Empty(t, temp)
		})
	}
}

func TestUpdateAlreadyLatest(t *testing.T) {
	oldVersion := C.Version
	C.Version = "v0.1.0"
	t.Cleanup(func() { C.Version = oldVersion })
	current := filepath.Join(t.TempDir(), "fluxgate")
	require.NoError(t, os.WriteFile(current, []byte("original"), 0o755))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/download/v0.1.0/checksums.txt" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/latest/download/version.txt" {
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		_, _ = io.WriteString(w, C.Version+"\n")
	}))
	defer server.Close()
	err := DefaultCoreUpdater.update(current, server.URL+"/", false)
	require.EqualError(t, err, "update error: already using latest version v0.1.0")
	err = DefaultCoreUpdater.update(current, server.URL+"/", true)
	require.ErrorContains(t, err, "get checksums:")
	content, err := os.ReadFile(current)
	require.NoError(t, err)
	require.Equal(t, "original", string(content))
}
