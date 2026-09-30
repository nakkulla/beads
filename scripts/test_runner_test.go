package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTestRunnerIsolation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exit   int
		nested bool
	}{
		{name: "coverage and cleanup"},
		{name: "failure cleanup", exit: 7},
		{name: "preserve inherited environment", nested: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			hostTmp := filepath.Join(root, "host-tmp")
			hooks := filepath.Join(root, "caller-hooks")
			outer := filepath.Join(root, "outer-env")
			for _, dir := range []string{bin, hostTmp, hooks, outer} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(hooks, "pre-commit"), "#!/bin/sh\ntouch \"$RUNNER_HOOK_FIRED\"\n")
			write(filepath.Join(bin, "dolt"), "#!/bin/sh\nexit 0\n")
			write(filepath.Join(bin, "go"), `#!/bin/sh
set -eu
case "$1" in
    env) printf '%s\n' "$RUNNER_ROOT/cache" ;;
    test)
        printf '%s\n' "$TMPDIR" > "$RUNNER_TRACE"
        shift
        profile=
        timeout=
        while [ "$#" -gt 0 ]; do
            case "$1" in
                -timeout) timeout=$2; shift ;;
                -coverprofile) profile=$2; shift ;;
            esac
            shift
        done
        [ "$timeout" = 10m ]
        mkdir "$TMPDIR/repo"
        cd "$TMPDIR/repo"
        git init -q
        git -c user.name=test -c user.email=test@example.invalid commit --allow-empty -qm fixture
        if git config --get test.inherited; then exit 9; fi
        printf 'coverage fixture\n' > "$profile"
        exit "$RUNNER_EXIT"
        ;;
    tool)
        [ -f "${3#-func=}" ]
        printf 'total: (statements) 42.0%%\n'
        ;;
    *) exit 8 ;;
esac
`)
			trace := filepath.Join(root, "trace")
			fired := filepath.Join(root, "hook-fired")
			cmd := exec.Command("bash", filepath.Join(sourceRepoRoot(t), "scripts", "test.sh"))
			env := os.Environ()
			for key, value := range map[string]string{
				"PATH":                     bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"TMPDIR":                   hostTmp,
				"GIT_CONFIG_COUNT":         "1",
				"GIT_CONFIG_KEY_0":         "core.hooksPath",
				"GIT_CONFIG_VALUE_0":       hooks,
				"GIT_CONFIG_PARAMETERS":    "'test.inherited=parent'",
				"BEADS_TEST_ENV_ACTIVE":    "0",
				"BEADS_TEST_ENV_DISABLE":   "0",
				"BEADS_TEST_ENV_KEEP":      "0",
				"BEADS_TEST_SHARED_SERVER": "0",
				"TEST_TIMEOUT":             "",
				"TEST_COVER":               "1",
				"TEST_COVERPROFILE":        "",
				"RUNNER_ROOT":              root,
				"RUNNER_TRACE":             trace,
				"RUNNER_HOOK_FIRED":        fired,
				"RUNNER_EXIT":              strconv.Itoa(tc.exit),
			} {
				env = replaceEnv(env, key, value)
			}
			if tc.nested {
				env = replaceEnv(env, "BEADS_TEST_ENV_ACTIVE", "1")
				env = replaceEnv(env, "BEADS_TEST_ENV_ROOT", outer)
			}
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.exit {
				t.Fatalf("runner: %v, want exit %d\n%s", err, tc.exit, out)
			}
			data, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			tmp := strings.TrimSpace(string(data))
			if strings.HasPrefix(tmp, hostTmp+string(filepath.Separator)) {
				t.Fatalf("runner temp dir %s is beneath crowded host TMPDIR", tmp)
			}
			if _, err := os.Stat(tmp); !os.IsNotExist(err) {
				t.Fatalf("runner temp dir not cleaned: %v", err)
			}
			if _, err := os.Stat(fired); !os.IsNotExist(err) {
				t.Fatalf("caller hook ran in test repository: %v", err)
			}
			if _, err := os.Stat(outer); err != nil {
				t.Fatalf("inherited environment removed: %v", err)
			}
			if tc.exit == 0 && !strings.Contains(string(out), "Total coverage: 42.0%") {
				t.Fatalf("coverage was not read before cleanup:\n%s", out)
			}
		})
	}
}
