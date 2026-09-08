package release_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"
)

var fixtureNumber uint64

type repository struct {
	root, work, remote, sha, script string
	env                             []string
}

func newRepository(t *testing.T) *repository {
	t.Helper()
	// Keep all disposable Git repositories inside the project, not the system temp directory.
	root, err := filepath.Abs(fmt.Sprintf(".release-contract-%d-%d", os.Getpid(), atomic.AddUint64(&fixtureNumber, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	script, err := filepath.Abs("../scripts/release.sh")
	if err != nil {
		t.Fatal(err)
	}
	r := &repository{
		root: root, work: filepath.Join(root, "work"), remote: filepath.Join(root, "origin.git"), script: script,
		env: []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + root, "LC_ALL=C",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=Release Test", "GIT_AUTHOR_EMAIL=release@example.invalid",
			"GIT_COMMITTER_NAME=Release Test", "GIT_COMMITTER_EMAIL=release@example.invalid",
		},
	}
	r.git(t, root, "init", "--quiet", "--bare", r.remote)
	r.git(t, root, "init", "--quiet", "--initial-branch=main", r.work)
	r.git(t, r.work, "commit", "--quiet", "--allow-empty", "-m", "initial build commit")
	r.sha = r.git(t, r.work, "rev-parse", "HEAD")
	r.git(t, r.work, "remote", "add", "origin", r.remote)
	r.git(t, r.work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	r.git(t, r.work, "checkout", "--quiet", "--detach", r.sha)
	return r
}

func (r *repository) git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repository) run(t *testing.T, mode string, env map[string]string) (map[string]string, string, error) {
	t.Helper()
	output := filepath.Join(r.root, "output")
	if err := os.Remove(output); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", r.script, mode)
	cmd.Dir = r.work
	cmd.Env = append(append([]string{}, r.env...), "GITHUB_OUTPUT="+output)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, runErr := cmd.CombinedOutput()
	values := map[string]string{}
	if data, err := os.ReadFile(output); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				t.Fatalf("invalid output line %q", line)
			}
			values[parts[0]] = parts[1]
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return values, string(out), runErr
}

func (r *repository) metadata(t *testing.T, event, ref, version, sha string) (map[string]string, string, error) {
	t.Helper()
	return r.run(t, "metadata", map[string]string{
		"GITHUB_EVENT_NAME": event, "GITHUB_REF": ref, "INPUT_VERSION": version, "GITHUB_SHA": sha,
	})
}

func (r *repository) ensureTag(t *testing.T, event, version string) (string, error) {
	t.Helper()
	_, out, err := r.run(t, "ensure-tag", map[string]string{
		"GITHUB_EVENT_NAME": event, "RELEASE_VERSION": version, "RELEASE_SHA": r.sha,
	})
	return out, err
}

func TestMetadata(t *testing.T) {
	r := newRepository(t)
	for _, tc := range []struct {
		name, event, ref, input, version, release, prerelease string
	}{
		{"first release", "workflow_dispatch", "refs/heads/main", "v0.1.0", "v0.1.0", "true", "false"},
		{"prerelease", "workflow_dispatch", "refs/heads/main", "v1.2.3-rc.1", "v1.2.3-rc.1", "true", "true"},
		{"semver identifiers", "workflow_dispatch", "refs/heads/main", "v1.2.3-0.alpha-1.2a", "v1.2.3-0.alpha-1.2a", "true", "true"},
		{"PR ignores input", "pull_request", "refs/pull/42/merge", "$(touch injected)", "v0.0.0-dev." + r.sha, "false", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 2; i++ {
				got, out, err := r.metadata(t, tc.event, tc.ref, tc.input, r.sha)
				if err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				want := map[string]string{
					"sha": r.sha, "version": tc.version, "package-version": strings.TrimPrefix(tc.version, "v"),
					"release": tc.release, "prerelease": tc.prerelease,
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("metadata = %#v, want %#v", got, want)
				}
			}
		})
	}
	if tags := r.git(t, r.work, "ls-remote", "--tags", "origin"); tags != "" {
		t.Fatalf("preflight must not create tags: %s", tags)
	}
}

func TestInvalidVersionsAreData(t *testing.T) {
	r := newRepository(t)
	for _, version := range []string{
		"", "1.2.3", "v1.2", "v1.2.3.4", "v01.2.3", "v1.02.3", "v1.2.03",
		"v1.2.3-", "v1.2.3-01", "v1.2.3-rc.01", "v1.2.3-rc..1", "v1.2.3+build",
		"v1.2.3-rc_1", " v1.2.3", "v1.2.3 ", "v1.2.3\nrelease=true",
		"v1.2.3;touch injected", "$(touch injected)", "`touch injected`", "v1.2.3-$(touch injected)",
		"--force", "v1.2.3/../../main",
	} {
		t.Run(version, func(t *testing.T) {
			for _, event := range []string{"workflow_dispatch", "push"} {
				got, out, err := r.metadata(t, event, "refs/tags/"+version, version, r.sha)
				if err == nil || len(got) != 0 {
					t.Fatalf("accepted %q: %#v\n%s", version, got, out)
				}
			}
			if out, err := r.ensureTag(t, "workflow_dispatch", version); err == nil {
				t.Fatalf("tagging accepted %q: %s", version, out)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(r.work, "injected")); !os.IsNotExist(err) {
		t.Fatalf("hostile version was executed: %v", err)
	}
}

func TestExistingTags(t *testing.T) {
	for _, annotated := range []bool{false, true} {
		t.Run(fmt.Sprintf("annotated=%v", annotated), func(t *testing.T) {
			r := newRepository(t)
			version := "v0.1.0"
			if annotated {
				version = "v0.1.0-rc.1"
				r.git(t, r.work, "tag", "-a", version, "-m", "release", r.sha)
			} else {
				r.git(t, r.work, "tag", version, r.sha)
			}
			r.git(t, r.work, "push", "--quiet", "origin", "refs/tags/"+version)
			tagObject := r.git(t, r.work, "rev-parse", "refs/tags/"+version)
			before := r.git(t, r.work, "ls-remote", "origin")
			for _, event := range []string{"workflow_dispatch", "push"} {
				// GitHub can supply either a tag object or the already peeled commit.
				for _, eventSHA := range []string{r.sha, tagObject} {
					got, out, err := r.metadata(t, event, "refs/tags/"+version, version, eventSHA)
					if err != nil || got["sha"] != r.sha || got["version"] != version || got["release"] != "true" {
						t.Fatalf("tag metadata = %#v: %v\n%s", got, err, out)
					}
				}
				for i := 0; i < 2; i++ {
					if out, err := r.ensureTag(t, event, version); err != nil {
						t.Fatalf("matching tag must be idempotent: %v\n%s", err, out)
					}
				}
			}
			if after := r.git(t, r.work, "ls-remote", "origin"); after != before {
				t.Fatalf("matching tag or branch was moved:\n%s\n%s", before, after)
			}
		})
	}
}

func TestBranchAdvancesAfterBuild(t *testing.T) {
	r := newRepository(t)
	got, out, err := r.metadata(t, "workflow_dispatch", "refs/heads/main", "v0.1.0", r.sha)
	if err != nil {
		t.Fatalf("first-release preflight: %v\n%s", err, out)
	}
	r.git(t, r.work, "checkout", "--quiet", "main")
	r.git(t, r.work, "commit", "--quiet", "--allow-empty", "-m", "branch advances after build")
	advanced := r.git(t, r.work, "rev-parse", "HEAD")
	r.git(t, r.work, "push", "--quiet", "origin", "main")
	r.git(t, r.work, "checkout", "--quiet", "--detach", got["sha"])
	if out, err := r.ensureTag(t, "workflow_dispatch", got["version"]); err != nil {
		t.Fatalf("first-release tagging: %v\n%s", err, out)
	}
	if tag := r.git(t, r.remote, "rev-parse", "refs/tags/v0.1.0"); tag != r.sha {
		t.Fatalf("tag = %s, want built commit %s", tag, r.sha)
	}
	if main := r.git(t, r.remote, "rev-parse", "refs/heads/main"); main != advanced {
		t.Fatalf("main moved: %s, want %s", main, advanced)
	}
	before := r.git(t, r.work, "ls-remote", "origin")
	if out, err := r.ensureTag(t, "workflow_dispatch", "v0.1.0"); err != nil {
		t.Fatalf("retry failed: %v\n%s", err, out)
	}
	if after := r.git(t, r.work, "ls-remote", "origin"); after != before {
		t.Fatal("retry changed remote refs")
	}
}

func TestMismatchedTagsNeverMove(t *testing.T) {
	for _, annotated := range []bool{false, true} {
		t.Run(fmt.Sprintf("annotated=%v", annotated), func(t *testing.T) {
			r := newRepository(t)
			// The tag can be created at a different commit while the matrix is running.
			if _, out, err := r.metadata(t, "workflow_dispatch", "refs/heads/main", "v0.1.0", r.sha); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			r.git(t, r.work, "commit", "--quiet", "--allow-empty", "-m", "different commit")
			if annotated {
				r.git(t, r.work, "tag", "-a", "v0.1.0", "-m", "different commit")
			} else {
				r.git(t, r.work, "tag", "v0.1.0")
			}
			r.git(t, r.work, "push", "--quiet", "origin", "refs/tags/v0.1.0")
			r.git(t, r.work, "checkout", "--quiet", "--detach", r.sha)
			before := r.git(t, r.work, "ls-remote", "origin")
			for _, event := range []string{"workflow_dispatch", "push"} {
				if got, out, err := r.metadata(t, event, "refs/tags/v0.1.0", "v0.1.0", r.sha); err == nil || len(got) != 0 {
					t.Fatalf("accepted mismatched tag: %#v\n%s", got, out)
				}
				if out, err := r.ensureTag(t, event, "v0.1.0"); err == nil {
					t.Fatalf("published mismatched tag: %s", out)
				}
			}
			if after := r.git(t, r.work, "ls-remote", "origin"); after != before {
				t.Fatal("rejected release changed remote refs")
			}
		})
	}
}

func TestFailuresAreNotReleases(t *testing.T) {
	r := newRepository(t)
	for _, tc := range []struct{ event, ref, sha string }{
		{"push", "refs/heads/main", r.sha},
		{"push", "refs/tags/v0.1.0", r.sha}, // Missing pushed tag.
		{"pull_request_target", "refs/heads/main", r.sha},
		{"workflow_dispatch", "refs/heads/main", "HEAD"},
		{"workflow_dispatch", "refs/heads/main", "$(touch injected)"},
		{"workflow_dispatch", "refs/heads/main", strings.Repeat("0", 40)},
	} {
		if got, out, err := r.metadata(t, tc.event, tc.ref, "v0.1.0", tc.sha); err == nil || len(got) != 0 {
			t.Fatalf("accepted %+v: %#v\n%s", tc, got, out)
		}
	}
	for _, event := range []string{"push", "pull_request", "pull_request_target"} {
		if out, err := r.ensureTag(t, event, "v0.1.0"); err == nil {
			t.Fatalf("published missing tag for %s: %s", event, out)
		}
	}
	r.git(t, r.work, "commit", "--quiet", "--allow-empty", "-m", "wrong checkout")
	if _, out, err := r.metadata(t, "workflow_dispatch", "refs/heads/main", "v0.1.0", r.sha); err == nil {
		t.Fatalf("accepted wrong checkout: %s", out)
	}
	if out, err := r.ensureTag(t, "workflow_dispatch", "v0.1.0"); err == nil {
		t.Fatalf("published wrong checkout: %s", out)
	}
	r.git(t, r.work, "checkout", "--quiet", "--detach", r.sha)
	// A remote failure must not be interpreted as an absent tag.
	r.git(t, r.work, "remote", "set-url", "origin", filepath.Join(r.root, "missing.git"))
	if got, out, err := r.metadata(t, "workflow_dispatch", "refs/heads/main", "v0.1.0", r.sha); err == nil || len(got) != 0 {
		t.Fatalf("ignored remote failure: %#v\n%s", got, out)
	}
	if out, err := r.ensureTag(t, "workflow_dispatch", "v0.1.0"); err == nil {
		t.Fatalf("ignored publication remote failure: %s", out)
	}
	// PR metadata must work entirely offline, without even an origin.
	got, out, err := r.metadata(t, "pull_request", "refs/pull/1/merge", "", r.sha)
	if err != nil || got["release"] != "false" {
		t.Fatalf("PR needs remote: %#v %v\n%s", got, err, out)
	}
}

func TestRejectedTagPushFails(t *testing.T) {
	r := newRepository(t)
	hook := filepath.Join(r.remote, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	before := r.git(t, r.work, "ls-remote", "origin")
	if out, err := r.ensureTag(t, "workflow_dispatch", "v0.1.0"); err == nil {
		t.Fatalf("rejected push reported success: %s", out)
	}
	if after := r.git(t, r.work, "ls-remote", "origin"); after != before {
		t.Fatal("rejected push changed remote refs")
	}
}

type workflowStep struct {
	Name string
	ID   string
	Uses string
	Run  string
	With map[string]string
	Env  map[string]string
}

type workflowJob struct {
	Needs       []string
	If          string
	Permissions map[string]string
	Outputs     map[string]string
	Env         map[string]string
	Steps       []workflowStep
	Strategy    struct {
		Matrix struct {
			Jobs []map[string]string
		}
	}
}

func TestWorkflowContract(t *testing.T) {
	data, err := os.ReadFile("../workflows/build.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On          map[string]yaml.Node
		Permissions map[string]string
		Jobs        map[string]workflowJob
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	equal := func(label string, got, want interface{}) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", label, got, want)
		}
	}
	equal("permissions", workflow.Permissions, map[string]string{"contents": "read"})
	equal("job count", len(workflow.Jobs), 4)
	equal("trigger count", len(workflow.On), 3)
	var push struct{ Tags, Branches []string }
	node := workflow.On["push"]
	if err := node.Decode(&push); err != nil {
		t.Fatal(err)
	}
	equal("push tags", push.Tags, []string{"v*"})
	equal("push branches", len(push.Branches), 0)
	var pr struct{ Branches []string }
	node = workflow.On["pull_request"]
	if err := node.Decode(&pr); err != nil {
		t.Fatal(err)
	}
	equal("PR target", pr.Branches, []string{"main"})
	var dispatch struct {
		Inputs map[string]struct {
			Required bool
			Type     string
		}
	}
	node = workflow.On["workflow_dispatch"]
	if err := node.Decode(&dispatch); err != nil {
		t.Fatal(err)
	}
	equal("required version", dispatch.Inputs["version"].Required, true)
	equal("version type", dispatch.Inputs["version"].Type, "string")

	step := func(job, name string) workflowStep {
		t.Helper()
		for _, s := range workflow.Jobs[job].Steps {
			if s.Name == name || s.Uses == name {
				return s
			}
		}
		t.Fatalf("missing %s step %s", job, name)
		return workflowStep{}
	}
	equal("metadata checkout", step("metadata", "actions/checkout@v6").With["ref"], "${{ github.sha }}")
	preflight := step("metadata", "Validate and capture release metadata")
	equal("metadata helper", preflight.Run, "bash .github/scripts/release.sh metadata")
	equal("version is environment data", preflight.Env["INPUT_VERSION"], "${{ inputs.version }}")
	equal("metadata output ID", preflight.ID, "release")
	equal("contract test step", step("metadata", "Test release contract").Run, "go test ./.github/tests")
	for index, name := range []string{"", "Validate and capture release metadata", "Test release contract"} {
		equal("preflight step order", workflow.Jobs["metadata"].Steps[index].Name, name)
	}
	for _, key := range []string{"sha", "version", "package-version", "release", "prerelease"} {
		equal("metadata output "+key, workflow.Jobs["metadata"].Outputs[key], "${{ steps.release.outputs."+key+" }}")
	}
	equal("build needs", workflow.Jobs["build"].Needs, []string{"metadata"})
	equal("release needs", workflow.Jobs["Upload-Release"].Needs, []string{"metadata", "build"})
	equal("Docker needs", workflow.Jobs["Docker"].Needs, []string{"metadata", "build", "Upload-Release"})
	equal("release gate", workflow.Jobs["Upload-Release"].If, "${{ needs.metadata.outputs.release == 'true' }}")
	equal("Docker gate", workflow.Jobs["Docker"].If, "${{ needs.metadata.outputs.release == 'true' && vars.ENABLE_DOCKER_PUBLISH == 'true' }}")
	equal("build version", workflow.Jobs["build"].Env["VERSION"], "${{ needs.metadata.outputs.version }}")
	equal("package version", workflow.Jobs["build"].Env["PackageVersion"], "${{ needs.metadata.outputs.package-version }}")
	for name, job := range workflow.Jobs {
		if name == "Upload-Release" {
			equal("release permissions", job.Permissions, map[string]string{"contents": "write"})
		} else {
			equal(name+" has no elevated permissions", len(job.Permissions), 0)
		}
		if name != "metadata" {
			equal(name+" immutable checkout", step(name, "actions/checkout@v6").With["ref"], "${{ needs.metadata.outputs.sha }}")
		}
		for _, s := range job.Steps {
			if strings.Contains(s.Run, "${{ inputs.") || strings.Contains(s.Run, "${{ github.event.inputs.") {
				t.Errorf("%s interpolates user input into shell", name)
			}
			if s.Run != "" {
				cmd := exec.Command("bash", "-n")
				cmd.Stdin = strings.NewReader(s.Run)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("%s/%s shell syntax: %v\n%s", name, s.Name, err, out)
				}
			}
		}
	}
	tag := step("Upload-Release", "Ensure tag points to the built commit")
	equal("tag helper", tag.Run, "bash .github/scripts/release.sh ensure-tag")
	equal("tag version", tag.Env["RELEASE_VERSION"], "${{ needs.metadata.outputs.version }}")
	equal("tag SHA", tag.Env["RELEASE_SHA"], "${{ needs.metadata.outputs.sha }}")
	releaseSteps := workflow.Jobs["Upload-Release"].Steps
	equal("tag guard precedes publication", releaseSteps[len(releaseSteps)-2].Name, tag.Name)
	equal("publication is last", releaseSteps[len(releaseSteps)-1].Name, "Upload Release")
	release := step("Upload-Release", "Upload Release")
	equal("release action", release.Uses, "softprops/action-gh-release@v3")
	equal("release version", release.With["tag_name"], "${{ needs.metadata.outputs.version }}")
	equal("release SHA", release.With["target_commitish"], "${{ needs.metadata.outputs.sha }}")
	equal("prerelease status", release.With["prerelease"], "${{ needs.metadata.outputs.prerelease == 'true' }}")
	equal("generated notes", release.With["generate_release_notes"], "true")
	equal("release files", release.With["files"], "bin/*")
	equal("missing artifacts fail", release.With["fail_on_unmatched_files"], "true")
	docker := step("Docker", "Extract Docker metadata")
	equal("Docker version", strings.TrimSpace(docker.With["tags"]), "type=raw,value=${{ needs.metadata.outputs.version }}")
	equal("stable latest only", strings.TrimSpace(docker.With["flavor"]), "latest=${{ needs.metadata.outputs.prerelease != 'true' }}")
	if !strings.Contains(docker.With["labels"], "org.opencontainers.image.revision=${{ needs.metadata.outputs.sha }}") {
		t.Error("Docker revision must use captured SHA")
	}
	equal("Docker publish", step("Docker", "Build and push Docker image").With["push"], "true")
	for _, forbidden := range []string{"ref: Meta", "Alpha", "--force", "write-all", "releases/latest", "git describe", "genReleaseNote.sh", "Upload-Prerelease"} {
		if strings.Contains(string(data), forbidden) {
			t.Errorf("obsolete or unsafe release wiring: %s", forbidden)
		}
	}
	if len(workflow.Jobs["build"].Strategy.Matrix.Jobs) == 0 {
		t.Error("missing compatibility build matrix")
	}
	if out, err := exec.Command("bash", "-n", "../scripts/release.sh").CombinedOutput(); err != nil {
		t.Fatalf("release helper syntax: %v\n%s", err, out)
	}
}
