package updater

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/component/ca"
	mihomoHttp "github.com/metacubex/mihomo/component/http"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/constant/features"
	"github.com/metacubex/mihomo/log"

	"github.com/metacubex/http"
)

const (
	coreReleaseURL = "https://github.com/JunZ-Leo/fluxgate-core/releases/"

	// MaxPackageFileSize bounds compressed downloads, including gVisor builds.
	MaxPackageFileSize = 128 * 1024 * 1024
	maxExecutableSize  = 512 * 1024 * 1024
	maxManifestSize    = 1024 * 1024

	ReleaseChannel = "release"
	AlphaChannel   = "alpha"
)

var stableVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// CoreUpdater updates Fluxgate without modifying another installed core.
// Originally adapted from AdGuardHome's internal/updater.
type CoreUpdater struct {
	mu sync.Mutex
}

var DefaultCoreUpdater = CoreUpdater{}

func (u *CoreUpdater) CoreBaseName() string {
	return coreBaseName(runtime.GOOS, runtime.GOARCH, features.GOARM, features.GOMIPS, features.GOAMD64)
}

func coreBaseName(goos, goarch, goarm, gomips, goamd64 string) string {
	base := C.ProductName + "-" + goos + "-" + goarch
	switch goarch {
	case "arm":
		level, _, _ := strings.Cut(goarm, ",")
		return base + "v" + level
	case "arm64":
		if goos == "android" {
			return base + "-v8"
		}
	case "mips", "mipsle":
		return base + "-" + gomips
	case "amd64":
		return base + "-" + goamd64
	}
	return base
}

func releaseSource(channel string) (string, error) {
	switch strings.ToLower(channel) {
	case "", "auto", ReleaseChannel:
		return coreReleaseURL, nil
	case AlphaChannel:
		return "", fmt.Errorf("Fluxgate self-update supports stable releases only; install prereleases manually")
	default:
		return "", fmt.Errorf("unsupported update channel: %s", channel)
	}
}

func (u *CoreUpdater) Update(currentExePath, channel string, force bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	source, err := releaseSource(channel)
	if err != nil {
		return err
	}
	return u.update(currentExePath, source, force)
}

func (u *CoreUpdater) update(currentExePath, source string, force bool) error {
	info, err := os.Stat(currentExePath)
	if err != nil {
		return fmt.Errorf("check currentExePath %q: %w", currentExePath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("current executable is not a regular file")
	}
	// Resolve a user-created executable symlink instead of replacing the link.
	currentExePath, err = filepath.EvalSymlinks(currentExePath)
	if err != nil {
		return err
	}
	version, err := u.getLatestVersion(source + "latest/download/version.txt")
	if err != nil {
		return fmt.Errorf("get latest version: %w", err)
	}
	if version == C.Version && !force {
		// Some controllers depend on this existing error text.
		return fmt.Errorf("update error: already using latest version %s", C.Version)
	}

	baseName := u.CoreBaseName()
	exeName := baseName
	extension := ".gz"
	if runtime.GOOS == "windows" {
		extension = ".zip"
		exeName += ".exe"
	}
	packageName := baseName + "-" + version + extension
	// Pin both the manifest and archive to the version we actually selected.
	versionURL := source + "download/" + version + "/"
	manifest, err := readCoreResource(versionURL+"checksums.txt", maxManifestSize)
	if err != nil {
		return fmt.Errorf("get checksums: %w", err)
	}
	expectedHash, err := checksumFor(manifest, packageName)
	if err != nil {
		return err
	}
	workDir := filepath.Dir(currentExePath)
	updateDir, err := os.MkdirTemp(workDir, ".fluxgate-update-")
	if err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(updateDir); err != nil {
			log.Warnln("updater: cleanup failed: %v", err)
		}
	}()
	packagePath := filepath.Join(updateDir, packageName)
	if err := u.download(packagePath, versionURL+packageName, expectedHash); err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	updateExePath, err := unpackCore(packagePath, updateDir, exeName, info.Mode())
	if err != nil {
		return fmt.Errorf("unpacking: %w", err)
	}
	backupDir := filepath.Join(workDir, "fluxgate-backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	backupPath := filepath.Join(backupDir, filepath.Base(currentExePath))
	if err := u.backup(currentExePath, backupPath); err != nil {
		return fmt.Errorf("backuping: %w", err)
	}
	if err := os.Rename(updateExePath, currentExePath); err != nil {
		if runtime.GOOS == "windows" {
			if restoreErr := os.Rename(backupPath, currentExePath); restoreErr != nil {
				return fmt.Errorf("replacing: %w; restoring backup: %w", err, restoreErr)
			}
		}
		return fmt.Errorf("replacing: %w", err)
	}
	log.Infoln("updater: updated Fluxgate to %s", version)
	return nil
}

func coreResponse(url string, timeout time.Duration) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	resp, err := mihomoHttp.HttpRequest(ctx, url, http.MethodGet, nil, nil, mihomoHttp.WithCAOption(ca.Option{ZeroTrust: true}))
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return resp, cancel, nil
}

func readCoreResource(url string, limit int64) ([]byte, error) {
	resp, cancel, err := coreResponse(url, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

func (u *CoreUpdater) getLatestVersion(url string) (string, error) {
	data, err := readCoreResource(url, 128)
	if err != nil {
		return "", err
	}
	version := strings.TrimRight(string(data), "\r\n")
	if !stableVersion.MatchString(version) {
		return "", fmt.Errorf("invalid stable Fluxgate version: %q", version)
	}
	return version, nil
}

func checksumFor(manifest []byte, name string) ([]byte, error) {
	var digest []byte
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./") != name {
			continue
		}
		if digest != nil {
			return nil, fmt.Errorf("duplicate checksum for %s", name)
		}
		var err error
		digest, err = hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("invalid SHA256 checksum for %s", name)
		}
	}
	if digest == nil {
		return nil, fmt.Errorf("checksum missing for %s", name)
	}
	return digest, nil
}

func copyBounded(dst io.Writer, src io.Reader, limit int64) error {
	n, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("content exceeds %d bytes", limit)
	}
	return nil
}

func (u *CoreUpdater) download(packagePath, url string, expectedHash []byte) error {
	resp, cancel, err := coreResponse(url, 90*time.Second)
	if err != nil {
		return err
	}
	defer cancel()
	defer resp.Body.Close()
	w, err := os.OpenFile(packagePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	copyErr := copyBounded(io.MultiWriter(w, hash), resp.Body, MaxPackageFileSize)
	closeErr := w.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !bytes.Equal(hash.Sum(nil), expectedHash) {
		return fmt.Errorf("SHA256 checksum mismatch")
	}
	return nil
}

func unpackCore(packagePath, outDir, expectedName string, mode os.FileMode) (string, error) {
	if strings.HasSuffix(packagePath, ".zip") {
		r, err := zip.OpenReader(packagePath)
		if err != nil {
			return "", err
		}
		defer r.Close()
		if len(r.File) != 1 || r.File[0].Name != expectedName || !r.File[0].Mode().IsRegular() {
			return "", fmt.Errorf("archive must contain only %s", expectedName)
		}
		entry, err := r.File[0].Open()
		if err != nil {
			return "", err
		}
		defer entry.Close()
		return writeExecutable(entry, outDir, expectedName, mode)
	}
	if !strings.HasSuffix(packagePath, ".gz") {
		return "", fmt.Errorf("unknown package extension")
	}
	f, err := os.Open(packagePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	input := bufio.NewReader(f)
	r, err := gzip.NewReader(input)
	if err != nil {
		return "", err
	}
	defer r.Close()
	r.Multistream(false)
	if r.Name != "" && r.Name != expectedName {
		return "", fmt.Errorf("archive must contain only %s", expectedName)
	}
	output, err := writeExecutable(r, outDir, expectedName, mode)
	if err != nil {
		return "", err
	}
	if _, err := input.Peek(1); err != io.EOF {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("archive contains trailing data or multiple gzip members")
	}
	return output, nil
}

func writeExecutable(r io.Reader, outDir, name string, mode os.FileMode) (string, error) {
	output := filepath.Join(outDir, name)
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return "", err
	}
	err = copyBounded(f, r, maxExecutableSize)
	if err == nil {
		err = f.Chmod(mode.Perm())
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return output, nil
}

func (u *CoreUpdater) backup(currentPath, backupPath string) error {
	if runtime.GOOS == "windows" {
		return os.Rename(currentPath, backupPath)
	}
	src, err := os.Open(currentPath)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	dst, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, src)
	closeErr := dst.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
