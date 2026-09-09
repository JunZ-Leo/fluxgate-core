package release_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFixtureFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func packagingJobs(t *testing.T) map[string]workflowJob {
	t.Helper()
	var workflow struct{ Jobs map[string]workflowJob }
	if err := yaml.Unmarshal([]byte(readContractFile(t, "../workflows/build.yml")), &workflow); err != nil {
		t.Fatal(err)
	}
	return workflow.Jobs
}

func packagingStep(t *testing.T, job workflowJob, name string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("missing step %s", name)
	return workflowStep{}
}

func requireContains(t *testing.T, contents string, required ...string) {
	t.Helper()
	for _, text := range required {
		if !strings.Contains(contents, text) {
			t.Errorf("missing %q", text)
		}
	}
}

func TestPackagingIdentity(t *testing.T) {
	job := packagingJobs(t)["build"]
	core := packagingStep(t, job, "Build core").Run
	requireContains(t, core, `-o "$BINARY"`, `-tags "with_gvisor"`,
		"github.com/metacubex/mihomo/constant.Version=${VERSION}")
	for _, tc := range []struct{ name, extension string }{
		{"Package DEB", "deb"}, {"Package RPM", "rpm"}, {"Package Pacman", "pkg.tar.zst"},
	} {
		run := packagingStep(t, job, tc.name).Run
		requireContains(t, run, `-v "${PackageVersion}"`,
			`-p "fluxgate-${{matrix.jobs.goos}}-${{matrix.jobs.output}}-${VERSION}.`+tc.extension+`"`,
			"fluxgate=/usr/bin/fluxgate", "cp .github/release/.fpm_systemd .fpm")
		if strings.Contains(run, "mihomo") {
			t.Errorf("%s still packages mihomo", tc.name)
		}
	}
	paths := packagingStep(t, job, "Archive production artifacts").With["path"]
	for _, extension := range []string{"gz", "zip", "deb", "rpm", "pkg.tar.zst"} {
		requireContains(t, paths, "fluxgate*."+extension)
	}
	requireContains(t, paths, "version.txt", "toolchain.tar.gz", "vendor.tar.gz")
	if strings.Contains(paths, "mihomo") {
		t.Error("upload still includes mihomo artifacts")
	}
	if strings.Contains(paths, "checksums.txt") {
		t.Error("matrix artifacts must not merge competing checksum manifests")
	}

	fpm := readContractFile(t, "../release/.fpm_systemd")
	requireContains(t, fpm,
		"--name fluxgate\n", "--license GPL-3.0-or-later\n",
		`--url "https://github.com/JunZ-Leo/fluxgate-core"`,
		`--maintainer "Fluxgate Core contributors"`,
		`--deb-field "Bug: https://github.com/JunZ-Leo/fluxgate-core/issues"`,
		"--config-files /etc/fluxgate/config.yaml\n",
		".github/release/config.yaml=/etc/fluxgate/config.yaml\n",
		".github/release/fluxgate.service=/usr/lib/systemd/system/fluxgate.service\n",
		".github/release/fluxgate@.service=/usr/lib/systemd/system/fluxgate@.service\n",
		"LICENSE=/usr/share/licenses/fluxgate/LICENSE",
		"NOTICE=/usr/share/licenses/fluxgate/NOTICE")
	for _, forbidden := range []string{"mihomo", "MetaCubeX", "--after-install", "--before-install", "--replaces", "--conflicts", "example.com"} {
		if strings.Contains(fpm, forbidden) {
			t.Errorf("unexpected package side effect or identity: %s", forbidden)
		}
	}
	capabilities := "CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_SYS_TIME CAP_SYS_PTRACE CAP_DAC_READ_SEARCH CAP_DAC_OVERRIDE"
	for _, tc := range []struct{ unit, home string }{
		{"fluxgate.service", "/etc/fluxgate"}, {"fluxgate@.service", "/etc/fluxgate/%i"},
	} {
		unit := readContractFile(t, "../release/"+tc.unit)
		requireContains(t, unit, "ExecStart=/usr/bin/fluxgate -d "+tc.home+"\n",
			"CapabilityBoundingSet="+capabilities+"\n", "AmbientCapabilities="+capabilities+"\n",
			"ExecReload=/bin/kill -HUP $MAINPID\n")
		if strings.Contains(unit, "mihomo") {
			t.Errorf("%s still uses mihomo identity", tc.unit)
		}
	}
	for _, removed := range []string{"../release/mihomo.service", "../release/mihomo@.service", "../workflows/trigger-cmfa-update.yml"} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Errorf("obsolete file still present: %s (%v)", removed, err)
		}
	}
	flake := readContractFile(t, "../../flake.nix")
	requireContains(t, flake, "packages.fluxgate = pkgs.fluxgate;", "packages.default = packages.fluxgate;",
		`pname = "fluxgate";`, "mv $out/bin/mihomo $out/bin/fluxgate")
	requireContains(t, readContractFile(t, "../../.gitignore"), "\n/fluxgate\n")
}

func TestReleaseChecksums(t *testing.T) {
	run := packagingStep(t, packagingJobs(t)["Upload-Release"], "Generate release checksums").Run
	root := newFixture(t)
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	generate := func() error {
		t.Helper()
		cmd := exec.Command("bash", "-c", run)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, out)
		}
		return nil
	}
	if err := generate(); err == nil {
		t.Fatal("empty release must not produce a successful manifest")
	}

	var gz, zipped bytes.Buffer
	g := gzip.NewWriter(&gz)
	g.Name = "fluxgate-linux-amd64-v1"
	if _, err := g.Write([]byte("linux fixture core")); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(&zipped)
	w, err := z.Create("fluxgate-windows-amd64-v1.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("windows fixture core")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"fluxgate-linux-amd64-v1-v0.1.0.gz":    gz.String(),
		"fluxgate-windows-amd64-v1-v0.1.0.zip": zipped.String(),
		"fluxgate-linux-amd64-v1-v0.1.0.deb":   "package fixture",
		"version.txt":                          "v0.1.0\n",
		"release note.txt":                     "fixture with a space in its name",
	}
	var names []string
	for name, data := range files {
		writeFixtureFile(t, filepath.Join(bin, name), data, 0600)
		names = append(names, name)
	}
	sort.Strings(names)
	var expected strings.Builder
	for _, name := range names {
		fmt.Fprintf(&expected, "%x  ./%s\n", sha256.Sum256([]byte(files[name])), name)
	}
	writeFixtureFile(t, filepath.Join(bin, "checksums.txt"), "stale manifest", 0600)
	for i := 0; i < 2; i++ {
		if err := generate(); err != nil {
			t.Fatal(err)
		}
		got := readContractFile(t, filepath.Join(bin, "checksums.txt"))
		if got != expected.String() {
			t.Fatalf("checksums = %q, want %q", got, expected.String())
		}
		if len(got) > 1<<20 {
			t.Fatal("manifest exceeds updater size limit")
		}
	}
	verify := exec.Command("sha256sum", "--check", "checksums.txt")
	verify.Dir = bin
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("manifest verification: %v\n%s", err, out)
	}
	writeFixtureFile(t, filepath.Join(bin, names[0]), "modified archive", 0600)
	verify = exec.Command("sha256sum", "--check", "checksums.txt")
	verify.Dir = bin
	if out, err := verify.CombinedOutput(); err == nil {
		t.Fatalf("modified archive passed verification:\n%s", out)
	}
}

// The workflow's compiler is replaced by a tiny executable fixture. ZIP is
// written by Go's standard library so testing packaging needs no zip package.
func TestWorkflowArchives(t *testing.T) {
	job := packagingJobs(t)["build"]
	build := packagingStep(t, job, "Build core").Run
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ goos, arch, version string }{
		{"linux", "amd64-v1", "v0.1.0"},
		{"linux", "armv7", "v0.1.0-rc.1"},
		{"darwin", "arm64-go120", "v0.1.0"},
		{"windows", "amd64-v1", "v0.1.0"},
		{"windows", "386-go120", "v0.1.0-rc.1"},
	} {
		t.Run(tc.goos+"-"+tc.arch, func(t *testing.T) {
			root := newFixture(t)
			tools := filepath.Join(root, "tools")
			if err := os.Mkdir(tools, 0700); err != nil {
				t.Fatal(err)
			}
			writeFixtureFile(t, filepath.Join(tools, "go"), `#!/bin/sh
set -eu
[ "$1" != env ] || exit 0
[ "$1" = build ]
printf '%s\n' "$@" > build-args
output=
while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then shift; output=$1; fi
    shift
done
case "$output" in fluxgate|fluxgate.exe) ;; *) exit 1 ;; esac
printf 'fixture core\n' > "$output"
`, 0700)
			writeFixtureFile(t, filepath.Join(tools, "zip"), `#!/bin/sh
exec "$ARCHIVE_TEST_BINARY" -test.run='^TestZIPFixtureTool$' -- "$@"
`, 0700)
			for _, legacy := range []string{"mihomo", "mihomo.exe"} {
				writeFixtureFile(t, filepath.Join(root, legacy), "untouched upstream", 0600)
			}
			run := strings.NewReplacer("${{matrix.jobs.goos}}", tc.goos, "${{matrix.jobs.output}}", tc.arch).Replace(build)
			cmd := exec.Command("bash", "-euo", "pipefail", "-c", run)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
				"VERSION="+tc.version, "BUILDTIME=fixture", "BUILDTAG=",
				"ARCHIVE_TEST_BINARY="+testBinary, "FLUXGATE_ZIP_FIXTURE=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("workflow fixture: %v\n%s", err, out)
			}
			member := "fluxgate-" + tc.goos + "-" + tc.arch
			archive := filepath.Join(root, member+"-"+tc.version)
			binary := "fluxgate"
			var reader io.ReadCloser
			if tc.goos == "windows" {
				binary += ".exe"
				member += ".exe"
				z, err := zip.OpenReader(archive + ".zip")
				if err != nil {
					t.Fatal(err)
				}
				defer z.Close()
				if len(z.File) != 1 || z.File[0].Name != member {
					t.Fatalf("unexpected zip member(s): %v", z.File)
				}
				reader, err = z.File[0].Open()
				if err != nil {
					t.Fatal(err)
				}
			} else {
				f, err := os.Open(archive + ".gz")
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				gz, err := gzip.NewReader(f)
				if err != nil {
					t.Fatal(err)
				}
				if gz.Name != member {
					t.Fatalf("gzip member = %q, want %q", gz.Name, member)
				}
				reader = gz
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || string(data) != "fixture core\n" {
				t.Fatalf("archive payload = %q (%v)", data, err)
			}
			requireContains(t, readContractFile(t, filepath.Join(root, "build-args")), "-o\n"+binary+"\n")
			if got := readContractFile(t, filepath.Join(root, binary)); got != string(data) {
				t.Fatal("archive does not contain standalone build output")
			}
			for _, legacy := range []string{"mihomo", "mihomo.exe"} {
				if got := readContractFile(t, filepath.Join(root, legacy)); got != "untouched upstream" {
					t.Fatalf("modified legacy binary %s", legacy)
				}
			}
		})
	}
}

func TestZIPFixtureTool(t *testing.T) {
	if os.Getenv("FLUXGATE_ZIP_FIXTURE") != "1" {
		return
	}
	args := os.Args[len(os.Args)-3:]
	if args[0] != "-r" {
		t.Fatalf("unexpected zip args: %v", args)
	}
	f, err := os.Create(args[1])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z := zip.NewWriter(f)
	w, err := z.Create(args[2])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, readContractFile(t, args[2])); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDockerArtifactSelection(t *testing.T) {
	script, err := filepath.Abs("../../docker/file-name.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := newFixture(t)
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	run := func(platform string) (string, error) {
		t.Helper()
		cmd := exec.Command("sh", script)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "TARGETPLATFORM="+platform)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	expectFailure := func(platform, message string) {
		t.Helper()
		if out, err := run(platform); err == nil || !strings.Contains(out, message) {
			t.Fatalf("selector %q = %q (%v), want %q failure", platform, out, err, message)
		}
	}
	expectFailure("linux/amd64", "missing bin/version.txt")
	for _, invalid := range []string{"", "v0.1.0/../../outside", "v0.1.0\nv0.2.0", "$(touch injected)"} {
		writeFixtureFile(t, filepath.Join(root, "bin/version.txt"), invalid, 0600)
		expectFailure("linux/amd64", "invalid version")
	}
	version := "v0.1.0-rc.1"
	writeFixtureFile(t, filepath.Join(root, "bin/version.txt"), version+"\n", 0600)
	matrix := packagingJobs(t)["build"].Strategy.Matrix.Jobs
	for platform, arch := range map[string]string{
		"linux/amd64": "amd64-v1", "linux/386": "386", "linux/arm64": "arm64",
		"linux/arm/v7": "armv7", "linux/riscv64": "riscv64",
	} {
		t.Run(platform, func(t *testing.T) {
			found := false
			for _, entry := range matrix {
				found = found || entry["goos"] == "linux" && entry["output"] == arch
			}
			if !found {
				t.Fatalf("Docker architecture %s has no release matrix artifact", arch)
			}
			artifact := "bin/fluxgate-linux-" + arch + "-" + version + ".gz"
			for _, decoy := range []string{
				strings.Replace(artifact, "fluxgate", "mihomo", 1),
				artifact + ".backup", strings.Replace(artifact, version, "v0.1.0-rcX1", 1),
			} {
				writeFixtureFile(t, filepath.Join(root, decoy), "wrong artifact", 0600)
			}
			expectFailure(platform, "missing artifact: "+artifact)
			writeFixtureFile(t, filepath.Join(root, artifact), "exact artifact", 0600)
			if out, err := run(platform); err != nil || out != artifact {
				t.Fatalf("selector = %q (%v), want %q", out, err, artifact)
			}
		})
	}
	for _, platform := range []string{"", "darwin/arm64", "linux/arm/v6", "linux/unknown"} {
		expectFailure(platform, "unsupported target platform")
	}
	docker := readContractFile(t, "../../Dockerfile")
	requireContains(t, docker, `FILE_NAME="$(sh file-name.sh)"`, `gzip -dc "$FILE_NAME" > fluxgate`,
		`VOLUME ["/root/.config/fluxgate/"]`, `ENTRYPOINT [ "/fluxgate" ]`,
		"COPY --from=builder /fluxgate/fluxgate /fluxgate",
		`org.opencontainers.image.source="https://github.com/JunZ-Leo/fluxgate-core"`,
		"https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/")
	for _, forbidden := range []string{"mihomo", "egrep", "awk NR==1", "ls bin/"} {
		if strings.Contains(docker, forbidden) {
			t.Errorf("Docker uses obsolete identity or ambiguous selection: %s", forbidden)
		}
	}
}

func TestMakeBuildIdentity(t *testing.T) {
	r := newRepository(t)
	makefile, err := filepath.Abs("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	r.git(t, r.work, "tag", "v0.1.0")
	short := r.git(t, r.work, "rev-parse", "--short", "HEAD")
	for _, tc := range []struct {
		args    []string
		version string
	}{
		{[]string{"BRANCH=main"}, "dev-" + short},
		{[]string{"BRANCH=main", "VERSION=v0.2.0"}, "v0.2.0"},
		{nil, "v0.1.0"},
	} {
		cmd := exec.Command("make", append([]string{"-n", "-f", makefile,
			"linux-amd64-v1.gz", "windows-amd64-v1.zip"}, tc.args...)...)
		cmd.Dir, cmd.Env = r.work, r.env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make dry-run: %v\n%s", err, out)
		}
		requireContains(t, string(out),
			"-o bin/fluxgate-linux-amd64-v1", "-o bin/fluxgate-windows-amd64-v1.exe",
			"constant.Version="+tc.version,
			fmt.Sprintf("gzip -f -S -%s.gz bin/fluxgate-linux-amd64-v1", tc.version),
			fmt.Sprintf("zip -m -j bin/fluxgate-windows-amd64-v1-%s.zip bin/fluxgate-windows-amd64-v1.exe", tc.version))
	}
}
