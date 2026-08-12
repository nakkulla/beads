package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	prWorkflowPath     = ".github/workflows/pr.yml"
	prRiskWorkflowPath = ".github/workflows/pr-risk.yml"
	mainWorkflowPath   = ".github/workflows/main.yml"
	prBaseExpression   = "${{ github.event.pull_request.base.sha || github.event.merge_group.base_sha }}"
	mainBaseExpression = "${{ github.event.before }}"
)

func TestEmbeddedStorageShardWorkflowContract(t *testing.T) {
	document := loadWorkflow(t, prRiskWorkflowPath)
	storage, ok := document.Jobs["test-embedded-storage"]
	if !ok {
		t.Fatal("test-embedded-storage job is missing")
	}
	if got := strings.TrimSpace(fmt.Sprint(storage["name"])); got != "Test (Embedded Dolt Storage ${{ matrix.shard }}/5)" {
		t.Errorf("storage display name = %q, want five-shard display name", got)
	}
	strategy, ok := storage["strategy"].(map[string]interface{})
	if !ok {
		t.Fatal("test-embedded-storage has no matrix strategy")
	}
	if got := fmt.Sprint(strategy["fail-fast"]); got != "false" {
		t.Errorf("storage fail-fast = %q, want false", got)
	}
	matrix, ok := strategy["matrix"].(map[string]interface{})
	if !ok || !sameStrings(stringList(matrix["shard"]), []string{"1", "2", "3", "4", "5"}) {
		t.Errorf("storage shard matrix = %v, want [1 2 3 4 5]", matrix["shard"])
	}
	if got := strings.TrimSpace(fmt.Sprint(storage["needs"])); got != "[detect-ci-tier build-embedded]" {
		t.Errorf("storage needs = %q, want unchanged owners", got)
	}
	if got := strings.TrimSpace(fmt.Sprint(storage["if"])); got != "needs.detect-ci-tier.outputs.full_embedded == 'true'" {
		t.Errorf("storage if = %q, want unchanged tier condition", got)
	}
	if _, exists := storage["timeout-minutes"]; exists {
		t.Error("storage job timeout changed; the runner owns the existing 20m test timeout")
	}
	if !strings.Contains(fmt.Sprint(storage), "bash .github/scripts/embedded-storage-test-shard.sh ${{ matrix.shard }} 5") {
		t.Error("storage job does not invoke the five-shard runner")
	}
	if !hasArtifactDownloadPath(storage, "embedded-test-binaries", "/tmp/") {
		t.Error("storage binary artifact download path changed")
	}
	runner, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), ".github/scripts/embedded-storage-test-shard.sh"))
	if err != nil || !strings.Contains(string(runner), "-test.timeout=20m") || !strings.Contains(string(runner), "/tmp/embeddeddolt-test") {
		t.Errorf("storage runner does not preserve the default binary and 20m test timeout: %v", err)
	}
	workflowRaw, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), prRiskWorkflowPath))
	if err != nil || !strings.Contains(string(workflowRaw), "if: always()") || !strings.Contains(string(workflowRaw), "retention-days: 7") {
		t.Error("storage job does not always upload seven-day shard artifacts")
	}
	for _, artifactPath := range []string{
		"artifacts/embedded-storage-shard-${{ matrix.shard }}-of-5-*",
		"artifacts/embedded-storage-shard-${{ matrix.shard }}-of-5.log",
	} {
		if !strings.Contains(string(workflowRaw), artifactPath) {
			t.Errorf("storage artifact upload does not cover %q", artifactPath)
		}
	}

	gate := document.Jobs["ci-gate"]
	if got := strings.TrimSpace(fmt.Sprint(gate["name"])); got != "CI Gate / Required" {
		t.Errorf("risk ci-gate name = %q, want CI Gate / Required", got)
	}
	if got := strings.TrimSpace(fmt.Sprint(gate["if"])); got != "${{ always() }}" {
		t.Errorf("risk ci-gate if = %q, want always", got)
	}
	if !sameStrings(stringList(gate["needs"]), []string{
		"detect-ci-tier", "build-embedded", "test-embedded-storage", "test-embedded-conformance", "test-server-storage", "test-embedded-cmd", "test-nix",
	}) {
		t.Errorf("risk ci-gate needs changed: %v", stringList(gate["needs"]))
	}
	gateEnv, ok := gateEvaluationEnv(gate)
	if !ok || fmt.Sprint(gateEnv["TEST_EMBEDDED_STORAGE"]) != "${{ needs.test-embedded-storage.result }}" {
		t.Errorf("risk ci-gate no longer owns the storage matrix result: %v", gateEnv)
	}
	if got := strings.Fields(fmt.Sprint(gateEnv["CI_GATE_REQUIRED"])); !sameStrings(got, []string{
		"DETECT_CI_TIER", "BUILD_EMBEDDED", "TEST_EMBEDDED_STORAGE", "TEST_EMBEDDED_CONFORMANCE", "TEST_SERVER_STORAGE", "TEST_EMBEDDED_CMD", "TEST_NIX",
	}) {
		t.Errorf("risk CI_GATE_REQUIRED = %v, want unchanged owner tokens", got)
	}
	if !strings.Contains(fmt.Sprint(gate), "CI_GATE_SKIPPED_OK") {
		t.Error("risk ci-gate skipped-owner calculation is missing")
	}
	for _, jobID := range []string{"test-embedded-conformance", "test-server-storage", "test-embedded-cmd", "test-nix"} {
		if _, exists := document.Jobs[jobID]; !exists {
			t.Errorf("required sibling job %q is missing", jobID)
		}
	}
}

func TestEmbeddedStorageShardRunnerContracts(t *testing.T) {
	fixture := newEmbeddedStorageShardFixture(t)
	assignments := make(map[string]string)
	for shard := 1; shard <= 5; shard++ {
		runner := filepath.Join(fixture.root, fmt.Sprintf("run-shard-%d.sh", shard))
		ciWriteExecutable(t, runner, fmt.Sprintf("#!/usr/bin/env bash\nexec %q %d 5 \"$@\"\n", fixture.sourceRunner, shard))
		artifactDir := filepath.Join(fixture.root, fmt.Sprintf("artifacts-%d", shard))
		output, status := runFixtureScript(t, fixture.root, runner, map[string]string{
			"BEADS_TEST_EMBEDDED_TEST_BINARY": fixture.binary,
			"BEADS_TEST_SHARD_MANIFEST":       fixture.manifest,
			"BEADS_TEST_SHARD_ARTIFACT_DIR":   artifactDir,
			"BEADS_TEST_SHARD_LIST_ONLY":      "1",
			"BEADS_TEST_EMBEDDED_DOLT":        "1",
			"FIXTURE_MODE":                    "pass",
		})
		if status != 0 {
			t.Fatalf("list-only shard %d status = %d\n%s", shard, status, output)
		}
		selected := readStorageSelected(t, filepath.Join(artifactDir, fmt.Sprintf("embedded-storage-shard-%d-of-5-selected.txt", shard)))
		for name, source := range selected {
			if previous, exists := assignments[name]; exists {
				t.Errorf("%s assigned to both shard %s and %d", name, previous, shard)
			}
			assignments[name] = fmt.Sprintf("%d:%s", shard, source)
		}
	}
	want := []string{"TestAlpha", "TestBeta", "TestDelta", "TestEpsilon", "TestGamma", "TestNew"}
	for _, excluded := range []string{"TestConformance", "TestHelperProcess"} {
		if _, exists := assignments[excluded]; exists {
			t.Errorf("special-owner test %s was selected", excluded)
		}
	}
	if got := sortedStorageNames(assignments); !sameStrings(got, want) {
		t.Errorf("selected test union = %v, want %v", got, want)
	}
	if !strings.HasSuffix(assignments["TestNew"], ":fallback") {
		t.Errorf("new test assignment = %q, want deterministic fallback", assignments["TestNew"])
	}
	repeatedFallback := ""
	for shard := 1; shard <= 5; shard++ {
		runner := filepath.Join(fixture.root, fmt.Sprintf("repeat-run-shard-%d.sh", shard))
		ciWriteExecutable(t, runner, fmt.Sprintf("#!/usr/bin/env bash\nexec %q %d 5 \"$@\"\n", fixture.sourceRunner, shard))
		artifactDir := filepath.Join(fixture.root, fmt.Sprintf("repeat-artifacts-%d", shard))
		output, status := runFixtureScript(t, fixture.root, runner, map[string]string{
			"BEADS_TEST_EMBEDDED_TEST_BINARY": fixture.binary,
			"BEADS_TEST_SHARD_MANIFEST":       fixture.manifest,
			"BEADS_TEST_SHARD_ARTIFACT_DIR":   artifactDir,
			"BEADS_TEST_SHARD_LIST_ONLY":      "1",
		})
		if status != 0 {
			t.Fatalf("repeated list-only shard %d status = %d\n%s", shard, status, output)
		}
		selected := readStorageSelected(t, filepath.Join(artifactDir, fmt.Sprintf("embedded-storage-shard-%d-of-5-selected.txt", shard)))
		if source, exists := selected["TestNew"]; exists {
			if repeatedFallback != "" {
				t.Fatalf("repeated fallback assigned TestNew to multiple shards: %s and %d", repeatedFallback, shard)
			}
			repeatedFallback = fmt.Sprintf("%d:%s", shard, source)
		}
	}
	if repeatedFallback != assignments["TestNew"] {
		t.Errorf("repeated fallback assignment = %q, want %q", repeatedFallback, assignments["TestNew"])
	}

	for name, manifest := range map[string]string{
		"duplicate":    "5 1 TestAlpha\n5 2 TestAlpha\n",
		"stale":        "5 1 TestMissing\n",
		"malformed":    "five 1 TestAlpha\n",
		"zero total":   "0 1 TestAlpha\n",
		"special":      "5 1 TestConformance\n",
		"out of range": "5 6 TestAlpha\n",
	} {
		t.Run(name, func(t *testing.T) {
			manifestPath := filepath.Join(t.TempDir(), "manifest.txt")
			if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			output, status := runFixtureScript(t, fixture.root, fixture.runner, map[string]string{
				"BEADS_TEST_EMBEDDED_TEST_BINARY": fixture.binary,
				"BEADS_TEST_SHARD_MANIFEST":       manifestPath,
				"BEADS_TEST_SHARD_ARTIFACT_DIR":   t.TempDir(),
				"BEADS_TEST_SHARD_LIST_ONLY":      "1",
			})
			if status == 0 {
				t.Fatalf("%s manifest unexpectedly succeeded\n%s", name, output)
			}
		})
	}
}

func TestEmbeddedStorageShardRunnerFailureContracts(t *testing.T) {
	fixture := newEmbeddedStorageShardFixture(t)
	artifactDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "test-binary-args")
	output, status := runFixtureScript(t, fixture.root, fixture.runner, map[string]string{
		"BEADS_TEST_EMBEDDED_TEST_BINARY": fixture.binary,
		"BEADS_TEST_SHARD_MANIFEST":       fixture.manifest,
		"BEADS_TEST_SHARD_ARTIFACT_DIR":   artifactDir,
		"BEADS_TEST_EMBEDDED_DOLT":        "1",
		"FIXTURE_MODE":                    "fail",
		"FIXTURE_ARGS":                    argsPath,
	})
	if status != 37 {
		t.Fatalf("failed test status = %d, want 37\n%s", status, output)
	}
	for _, filename := range []string{
		"embedded-storage-shard-1-of-5-selected.txt",
		"embedded-storage-shard-1-of-5-timing.tsv",
		"embedded-storage-shard-1-of-5-summary.txt",
		"embedded-storage-shard-1-of-5.log",
	} {
		if _, err := os.Stat(filepath.Join(artifactDir, filename)); err != nil {
			t.Errorf("failure artifact %s is missing: %v", filename, err)
		}
	}
	if summary := readCIFile(t, filepath.Join(artifactDir, "embedded-storage-shard-1-of-5-summary.txt")); !strings.Contains(summary, "process_exit_status=37") {
		t.Errorf("failure summary does not preserve test status:\n%s", summary)
	}
	if args := readCIFile(t, argsPath); !strings.Contains(args, "-test.run ^(") || !strings.HasSuffix(args, ")$\n") || strings.Contains(args, "TestConformance") || strings.Contains(args, "TestHelperProcess") {
		t.Errorf("runner test selection is not an exact top-level regex: %q", args)
	}

	parseArtifactDir := t.TempDir()
	output, status = runFixtureScript(t, fixture.root, fixture.runner, map[string]string{
		"BEADS_TEST_EMBEDDED_TEST_BINARY": fixture.binary,
		"BEADS_TEST_SHARD_MANIFEST":       fixture.manifest,
		"BEADS_TEST_SHARD_ARTIFACT_DIR":   parseArtifactDir,
		"FIXTURE_MODE":                    "unparseable",
	})
	if status != 0 {
		t.Fatalf("unparseable timing log status = %d, want test success preserved\n%s", status, output)
	}
	if summary := readCIFile(t, filepath.Join(parseArtifactDir, "embedded-storage-shard-1-of-5-summary.txt")); !strings.Contains(summary, "timing_parse=unavailable") {
		t.Errorf("unparseable timing summary = %q, want unavailable", summary)
	}
}

func TestEmbeddedStorageShardRunnerFailClosedContracts(t *testing.T) {
	fixture := newEmbeddedStorageShardFixture(t)
	for name, env := range map[string]map[string]string{
		"missing binary": {
			"BEADS_TEST_EMBEDDED_TEST_BINARY": filepath.Join(t.TempDir(), "missing-test-binary"),
		},
		"inventory failure": {
			"BEADS_TEST_EMBEDDED_TEST_BINARY": fixture.binary,
			"FIXTURE_MODE":                    "list-fail",
		},
		"empty inventory": {
			"BEADS_TEST_EMBEDDED_TEST_BINARY": fixture.binary,
			"FIXTURE_MODE":                    "empty-list",
		},
	} {
		t.Run(name, func(t *testing.T) {
			env["BEADS_TEST_SHARD_MANIFEST"] = fixture.manifest
			env["BEADS_TEST_SHARD_ARTIFACT_DIR"] = t.TempDir()
			output, status := runFixtureScript(t, fixture.root, fixture.runner, env)
			if status == 0 {
				t.Fatalf("%s unexpectedly succeeded\n%s", name, output)
			}
		})
	}
}

func TestEmbeddedStorageShardManifestBalanceContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), ".github/scripts/embedded-storage-test-shards.txt"))
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	total := 0
	previousName := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) != 3 || fields[0] != "5" {
			t.Fatalf("manifest row %q, want active three-column 5-shard row", line)
		}
		if fields[2] == "TestConformance" || fields[2] == "TestHelperProcess" {
			t.Fatalf("manifest assigns special-owner test %s", fields[2])
		}
		if previousName != "" && fields[2] <= previousName {
			t.Fatalf("manifest test names are not strictly sorted: %s before %s", previousName, fields[2])
		}
		previousName = fields[2]
		counts[fields[1]]++
		total++
	}
	if total != 57 {
		t.Errorf("active manifest roots = %d, want 57", total)
	}
	if got := []int{counts["1"], counts["2"], counts["3"], counts["4"], counts["5"]}; fmt.Sprint(got) != "[6 9 10 19 13]" {
		t.Errorf("manifest shard counts = %v, want [6 9 10 19 13]", got)
	}
}

type workflowDocument struct {
	Jobs map[string]map[string]interface{} `yaml:"jobs"`
}

func TestCIWorkflowOwnershipContracts(t *testing.T) {
	workflowCases := []struct {
		name             string
		path             string
		standaloneJobIDs []string
	}{
		{
			name: "pr",
			path: prWorkflowPath,
			standaloneJobIDs: []string{
				"check-build-tags",
				"check-version-consistency",
				"check-doc-flags",
				"check-no-beads-changes",
				"fmt-check",
				"lint",
			},
		},
		{
			name: "main",
			path: mainWorkflowPath,
			standaloneJobIDs: []string{
				"check-build-tags",
				"check-version-consistency",
				"check-doc-flags",
				"check-no-duplicate-migrations",
				"fmt-check",
				"lint",
			},
		},
	}

	for _, workflowCase := range workflowCases {
		workflowCase := workflowCase
		t.Run(workflowCase.name, func(t *testing.T) {
			document := loadWorkflow(t, workflowCase.path)
			artifactJob, ok := document.Jobs["build-artifacts"]
			if !ok {
				t.Fatal("build-artifacts job is missing")
			}
			for _, forbidden := range []string{
				"golangci-lint",
				"make ci-pr-policy",
				"make ci-pr-lint",
			} {
				if countScalarSubstring(artifactJob, forbidden) != 0 {
					t.Errorf("build-artifacts contains forbidden %q", forbidden)
				}
			}

			for _, jobID := range workflowCase.standaloneJobIDs {
				if _, exists := document.Jobs[jobID]; exists {
					t.Errorf("approved standalone job %q is still present", jobID)
				}
			}

			commands := map[string]string{
				"make ci-pr-policy": "pr-policy-wrapper",
				"make ci-pr-lint":   "pr-lint-wrapper",
				"make ci-pr-core":   "pr-core-wrapper",
			}
			for command, owner := range commands {
				var owners []string
				total := 0
				for jobID, job := range document.Jobs {
					count := countScalarSubstring(job, command)
					total += count
					if count != 0 {
						owners = append(owners, jobID)
					}
				}
				if total != 1 || len(owners) != 1 || owners[0] != owner {
					t.Errorf("%s owner = %v with %d occurrences, want only %s once", command, owners, total, owner)
				}
			}
		})
	}
}

func TestPRCIGateContract(t *testing.T) {
	document := loadWorkflow(t, prWorkflowPath)
	gate, ok := document.Jobs["ci-gate"]
	if !ok {
		t.Fatal("ci-gate job is missing")
	}

	if got := strings.TrimSpace(fmt.Sprint(gate["name"])); got != "CI Gate / Required" {
		t.Errorf("ci-gate name = %q, want %q", got, "CI Gate / Required")
	}
	if got := strings.TrimSpace(fmt.Sprint(gate["if"])); got != "${{ always() }}" {
		t.Errorf("ci-gate if = %q, want ${{ always() }}", got)
	}

	wantOwners := []string{
		"build-artifacts",
		"check-cmd-bd-puregeo-tests",
		"check-migration-hygiene",
		"detect-package-gates",
		"package-mcp",
		"package-npm",
		"package-website",
		"pr-policy-wrapper",
		"pr-core-wrapper",
		"pr-lint-wrapper",
		"test-domain-uow",
	}
	wantTokens := make([]string, 0, len(wantOwners))
	for _, owner := range wantOwners {
		wantTokens = append(wantTokens, strings.ToUpper(strings.ReplaceAll(owner, "-", "_")))
	}

	needs := stringList(gate["needs"])
	if !sameStrings(needs, wantOwners) {
		t.Errorf("ci-gate needs = %v, want %v", needs, wantOwners)
	}

	gateEnv, ok := gateEvaluationEnv(gate)
	if !ok {
		t.Fatal("ci-gate evaluation step with CI_GATE_REQUIRED is missing")
	}
	required := strings.Fields(strings.TrimSpace(fmt.Sprint(gateEnv["CI_GATE_REQUIRED"])))
	if !sameStrings(required, wantTokens) {
		t.Errorf("CI_GATE_REQUIRED = %v, want %v", required, wantTokens)
	}

	wantResultKeys := make(map[string]bool, len(wantTokens))
	for i, token := range wantTokens {
		wantResultKeys[token] = true
		want := fmt.Sprintf("${{ needs.%s.result }}", wantOwners[i])
		got, exists := gateEnv[token]
		if !exists || strings.TrimSpace(fmt.Sprint(got)) != want {
			t.Errorf("%s = %q, want %q", token, got, want)
		}
	}
	for key := range gateEnv {
		if key == "CI_GATE_NAME" || key == "CI_GATE_REQUIRED" {
			continue
		}
		if !wantResultKeys[key] {
			t.Errorf("ci-gate has result env %q outside the required owner mapping", key)
		}
	}

	if countScalarSubstring(gate, "CI_GATE_SKIPPED_OK") != 0 {
		t.Error("ci-gate retains a baseline skipped allowlist")
	}
}

func TestCIWorkflowEventAndDiagnosticContracts(t *testing.T) {
	pr := loadWorkflow(t, prWorkflowPath)
	main := loadWorkflow(t, mainWorkflowPath)

	assertPolicyBaseEnv(t, pr, prBaseExpression)
	assertPolicyBaseEnv(t, main, mainBaseExpression)

	prPolicy := pr.Jobs["pr-policy-wrapper"]
	prPolicyStepEnv, ok := stepEnvForRun(prPolicy, "make ci-pr-policy")
	if !ok {
		t.Error("pr-policy-wrapper has no make ci-pr-policy step")
	} else {
		assertEnvValue(t, prPolicyStepEnv, "DOC_DRIFT_PATCH_OUT", "${{ runner.temp }}/cli-docs-freshness.patch")
	}
	if !hasFailureUpload(prPolicy, "cli-docs-freshness-patch") {
		t.Error("pr-policy-wrapper has no failure-only cli-docs-freshness-patch upload")
	}

	for _, workflowCase := range []struct {
		name      string
		document  workflowDocument
		baseValue string
	}{
		{name: "pr", document: pr, baseValue: prBaseExpression},
		{name: "main", document: main, baseValue: mainBaseExpression},
	} {
		workflowCase := workflowCase
		t.Run(workflowCase.name+" migration hygiene", func(t *testing.T) {
			job, ok := workflowCase.document.Jobs["check-migration-hygiene"]
			if !ok {
				t.Fatal("check-migration-hygiene job is missing")
			}
			env, ok := stepEnvForRun(job, "check-migration-hygiene.sh")
			if !ok {
				t.Fatal("check-migration-hygiene invocation has no environment")
			}
			assertEnvValue(t, env, "BASE_SHA", workflowCase.baseValue)
		})
	}

	if _, exists := main.Jobs["check-no-duplicate-migrations"]; exists {
		t.Error("main workflow retains the inline duplicate-migration scan")
	}
}

func TestCITimeAccumulate(t *testing.T) {
	fixture := newTimingFixture(t)
	output, status := runFixtureScript(t, fixture.root, fixture.script, map[string]string{
		"FIXTURE_ROOT":   fixture.root,
		"FIXTURE_LOG":    fixture.log,
		"FIXTURE_PATCH":  fixture.patch,
		"FIXTURE_RESULT": fixture.result,
	})
	if status == 42 && markerExists(t, fixture.log, "helper-missing") {
		t.Fatalf("ci_time_accumulate contract is absent; timing.sh did not provide the approved accumulator\n%s", output)
	}
	if status != 7 {
		t.Fatalf("accumulator fixture status = %d, want first nonzero 7\n%s", status, output)
	}
	markers := readMarkers(t, fixture.log)
	for _, marker := range []string{"first-failure", "later-success"} {
		if !markers[marker] {
			t.Errorf("accumulator fixture did not run %q; output:\n%s", marker, output)
		}
	}
	if got := strings.TrimSpace(readCIFile(t, fixture.result)); got != "status=7 first=0 second=0" {
		t.Errorf("accumulator result = %q, want status=7 first=0 second=0", got)
	}
	if got := strings.TrimSpace(readCIFile(t, fixture.patch)); got != "docs patch" {
		t.Errorf("accumulator patch = %q, want docs patch", got)
	}
}

func TestCIPolicyAggregation(t *testing.T) {
	fixture := newPolicyFixture(t)
	output, status := runFixtureScript(t, fixture.root, fixture.script, map[string]string{
		"FIXTURE_ROOT":               fixture.root,
		"FIXTURE_LOG":                fixture.log,
		"DOC_DRIFT_PATCH_OUT":        fixture.patch,
		"GITHUB_BASE_REF":            "main",
		"GITHUB_STEP_SUMMARY":        fixture.summary,
		"CI_SKIP_BEADS_CHANGE_CHECK": "0",
		"PATH":                       fixture.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	if strings.Contains(strings.ToLower(output), "command not found") {
		t.Fatalf("policy fixture failed while discovering a fake command, not at the contract seam:\n%s", output)
	}
	if status == 0 {
		t.Fatalf("policy aggregation unexpectedly succeeded after the docs binary producer failed\n%s", output)
	}
	markers := readMarkers(t, fixture.log)
	for _, marker := range []string{
		"check-build-tags",
		"check-go-install-guidance",
		"check-versions",
		"docs-binary",
		"check-doc-freshness",
		"check-testing-short",
		"check-beads",
	} {
		if !markers[marker] {
			t.Errorf("policy aggregation did not reach independent check %q; output:\n%s", marker, output)
		}
	}
	if markers["check-doc-flags"] {
		t.Error("policy aggregation ran binary-dependent doc flags after the docs binary producer failed")
	}
	patch, err := os.ReadFile(fixture.patch)
	if err != nil {
		t.Errorf("policy docs patch was not preserved after producer failure: %v", err)
	} else if got := strings.TrimSpace(string(patch)); got != "docs patch" {
		t.Errorf("policy docs patch = %q, want docs patch", got)
	}
}

func TestCILintAggregation(t *testing.T) {
	fixture := newLintFixture(t)
	output, status := runFixtureScript(t, fixture.root, fixture.script, map[string]string{
		"FIXTURE_ROOT":        fixture.root,
		"FIXTURE_LOG":         fixture.log,
		"GITHUB_STEP_SUMMARY": fixture.summary,
		"PATH":                fixture.bin + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	if strings.Contains(strings.ToLower(output), "command not found") {
		t.Fatalf("lint fixture failed while discovering a fake command, not at the contract seam:\n%s", output)
	}
	if status == 0 {
		t.Fatalf("lint aggregation unexpectedly succeeded after fmt-check failed\n%s", output)
	}
	markers := readMarkers(t, fixture.log)
	if !markers["fmt-check"] {
		t.Errorf("lint fixture did not run fmt-check; output:\n%s", output)
	}
	if !markers["golangci-lint"] {
		t.Errorf("lint fixture stopped before golangci-lint after fmt-check failed; output:\n%s", output)
	}
	summary := readCIFile(t, fixture.summary)
	for _, label := range []string{"gofmt check", "golangci-lint"} {
		if !strings.Contains(summary, label) {
			t.Errorf("lint timing summary is missing %q:\n%s", label, summary)
		}
	}
}

func loadWorkflow(t *testing.T, relativePath string) workflowDocument {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), relativePath))
	if err != nil {
		t.Fatalf("read workflow %s: %v", relativePath, err)
	}
	var document workflowDocument
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse workflow %s: %v", relativePath, err)
	}
	return document
}

func assertPolicyBaseEnv(t *testing.T, document workflowDocument, want string) {
	t.Helper()
	job, ok := document.Jobs["pr-policy-wrapper"]
	if !ok {
		t.Fatal("pr-policy-wrapper job is missing")
	}
	env, ok := stepEnvForRun(job, "make ci-pr-policy")
	if !ok {
		t.Error("pr-policy-wrapper has no make ci-pr-policy step")
		return
	}
	assertEnvValue(t, env, "BD_DOCS_DIFF_BASE", want)
	assertEnvValue(t, env, "CI_BEADS_DIFF_BASE", want)
}

func assertEnvValue(t *testing.T, env map[string]interface{}, key, want string) {
	t.Helper()
	got, ok := env[key]
	if !ok || strings.TrimSpace(fmt.Sprint(got)) != want {
		t.Errorf("environment %s = %q, want %q", key, got, want)
	}
}

func gateEvaluationEnv(job map[string]interface{}) (map[string]interface{}, bool) {
	steps, ok := job["steps"].([]interface{})
	if !ok {
		return nil, false
	}
	for _, rawStep := range steps {
		step, ok := rawStep.(map[string]interface{})
		if !ok {
			continue
		}
		env, ok := step["env"].(map[string]interface{})
		if ok {
			if _, exists := env["CI_GATE_REQUIRED"]; exists {
				return env, true
			}
		}
	}
	return nil, false
}

func stepEnvForRun(job map[string]interface{}, command string) (map[string]interface{}, bool) {
	steps, ok := job["steps"].([]interface{})
	if !ok {
		return nil, false
	}
	for _, rawStep := range steps {
		step, ok := rawStep.(map[string]interface{})
		if !ok {
			continue
		}
		run, _ := step["run"].(string)
		if !strings.Contains(run, command) {
			continue
		}
		env, _ := step["env"].(map[string]interface{})
		return env, true
	}
	return nil, false
}

func hasFailureUpload(job map[string]interface{}, artifactName string) bool {
	steps, ok := job["steps"].([]interface{})
	if !ok {
		return false
	}
	for _, rawStep := range steps {
		step, ok := rawStep.(map[string]interface{})
		if !ok {
			continue
		}
		if !strings.Contains(fmt.Sprint(step["uses"]), "actions/upload-artifact") {
			continue
		}
		if condition := strings.TrimSpace(fmt.Sprint(step["if"])); condition != "failure()" && condition != "${{ failure() }}" {
			continue
		}
		with, _ := step["with"].(map[string]interface{})
		if strings.TrimSpace(fmt.Sprint(with["name"])) == artifactName {
			return true
		}
	}
	return false
}

func hasArtifactDownloadPath(job map[string]interface{}, artifactName, path string) bool {
	steps, ok := job["steps"].([]interface{})
	if !ok {
		return false
	}
	for _, rawStep := range steps {
		step, ok := rawStep.(map[string]interface{})
		if !ok || !strings.Contains(fmt.Sprint(step["uses"]), "actions/download-artifact") {
			continue
		}
		with, _ := step["with"].(map[string]interface{})
		if fmt.Sprint(with["name"]) == artifactName && fmt.Sprint(with["path"]) == path {
			return true
		}
	}
	return false
}

func countScalarSubstring(value interface{}, substring string) int {
	count := 0
	for _, scalar := range scalarStrings(value) {
		count += strings.Count(scalar, substring)
	}
	return count
}

func scalarStrings(value interface{}) []string {
	switch value := value.(type) {
	case string:
		return []string{value}
	case []interface{}:
		var result []string
		for _, item := range value {
			result = append(result, scalarStrings(item)...)
		}
		return result
	case map[string]interface{}:
		var result []string
		for key, item := range value {
			result = append(result, key)
			result = append(result, scalarStrings(item)...)
		}
		return result
	case map[interface{}]interface{}:
		var result []string
		for key, item := range value {
			result = append(result, fmt.Sprint(key))
			result = append(result, scalarStrings(item)...)
		}
		return result
	default:
		return nil
	}
}

func stringList(value interface{}) []string {
	var result []string
	for _, item := range stringSliceValues(value) {
		result = append(result, item)
	}
	return result
}

func stringSliceValues(value interface{}) []string {
	switch value := value.(type) {
	case []interface{}:
		result := make([]string, 0, len(value))
		for _, item := range value {
			result = append(result, fmt.Sprint(item))
		}
		return result
	case string:
		return strings.Fields(value)
	default:
		return nil
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type ciFixture struct {
	root    string
	script  string
	log     string
	patch   string
	result  string
	summary string
	bin     string
}

func newTimingFixture(t *testing.T) ciFixture {
	t.Helper()
	root := t.TempDir()
	timingPath := filepath.Join(root, "scripts", "ci", "lib", "timing.sh")
	copyCISourceFile(t, filepath.Join(sourceRepoRoot(t), "scripts", "ci", "lib", "timing.sh"), timingPath, 0o755)
	log := filepath.Join(root, "markers.log")
	patch := filepath.Join(root, "docs.patch")
	result := filepath.Join(root, "result.txt")
	script := filepath.Join(root, "run.sh")
	ciWriteExecutable(t, script, `#!/usr/bin/env bash
set -euo pipefail
source "$FIXTURE_ROOT/scripts/ci/lib/timing.sh"

if ! declare -F ci_time_accumulate >/dev/null; then
    printf '%s\n' helper-missing >> "$FIXTURE_LOG"
    exit 42
fi

status=0
ci_time_accumulate status first-failure -- sh -c 'printf "%s\n" first-failure >> "$FIXTURE_LOG"; exit 7'
first=$?
ci_time_accumulate status later-success -- sh -c 'printf "%s\n" later-success >> "$FIXTURE_LOG"; printf "%s\n" "docs patch" > "$FIXTURE_PATCH"'
second=$?
printf 'status=%s first=%s second=%s\n' "$status" "$first" "$second" > "$FIXTURE_RESULT"
exit "$status"
`)
	return ciFixture{root: root, script: script, log: log, patch: patch, result: result}
}

func newPolicyFixture(t *testing.T) ciFixture {
	t.Helper()
	root := t.TempDir()
	copyCISourceFile(t, filepath.Join(sourceRepoRoot(t), "scripts", "ci", "pr-policy.sh"), filepath.Join(root, "scripts", "ci", "pr-policy.sh"), 0o755)
	copyCISourceFile(t, filepath.Join(sourceRepoRoot(t), "scripts", "ci", "lib", "timing.sh"), filepath.Join(root, "scripts", "ci", "lib", "timing.sh"), 0o755)
	if err := os.WriteFile(filepath.Join(root, ".buildflags"), []byte("BEADS_BUILD_TAGS=gms_pure_go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	log := filepath.Join(root, "markers.log")
	patch := filepath.Join(root, "docs.patch")
	summary := filepath.Join(root, "summary.md")
	bin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ciWriteExecutable(t, filepath.Join(bin, "git"), `#!/bin/sh
set -eu
case "${1:-} ${2:-} ${3:-}" in
  "rev-parse --short HEAD") printf '%s\n' fixture-head ;;
  "rev-parse --verify --quiet") exit 0 ;;
  "diff --name-only "*) printf '%s\n' check-beads >> "$FIXTURE_LOG" ;;
  *) echo "unexpected fake git invocation: $*" >&2; exit 97 ;;
esac
`)
	ciWriteExecutable(t, filepath.Join(bin, "go"), `#!/bin/sh
set -eu
if [ "${1:-}" = build ]; then
    printf '%s\n' docs-binary >> "$FIXTURE_LOG"
    exit 23
fi
echo "unexpected fake go invocation: $*" >&2
exit 97
`)
	for name, marker := range map[string]string{
		"check-build-tags.sh":          "check-build-tags",
		"check-go-install-guidance.sh": "check-go-install-guidance",
		"check-versions.sh":            "check-versions",
		"check-doc-flags.sh":           "check-doc-flags",
		"check-testing-short.sh":       "check-testing-short",
	} {
		ciWriteExecutable(t, filepath.Join(root, "scripts", name), fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q >> \"$FIXTURE_LOG\"\n", marker))
	}
	ciWriteExecutable(t, filepath.Join(root, "scripts", "check-doc-freshness.sh"), `#!/bin/sh
set -eu
printf '%s\n' check-doc-freshness >> "$FIXTURE_LOG"
printf '%s\n' 'docs patch' > "$DOC_DRIFT_PATCH_OUT"
`)
	ciWriteExecutable(t, filepath.Join(root, "run.sh"), `#!/usr/bin/env bash
exec "$FIXTURE_ROOT/scripts/ci/pr-policy.sh"
`)
	return ciFixture{root: root, script: filepath.Join(root, "run.sh"), log: log, patch: patch, summary: summary, bin: bin}
}

func newLintFixture(t *testing.T) ciFixture {
	t.Helper()
	root := t.TempDir()
	copyCISourceFile(t, filepath.Join(sourceRepoRoot(t), "scripts", "ci", "pr-lint.sh"), filepath.Join(root, "scripts", "ci", "pr-lint.sh"), 0o755)
	copyCISourceFile(t, filepath.Join(sourceRepoRoot(t), "scripts", "ci", "lib", "timing.sh"), filepath.Join(root, "scripts", "ci", "lib", "timing.sh"), 0o755)
	if err := os.WriteFile(filepath.Join(root, ".buildflags"), []byte("BEADS_BUILD_TAGS=gms_pure_go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "markers.log")
	summary := filepath.Join(root, "summary.md")
	bin := filepath.Join(root, "fake-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(`.PHONY: fmt-check
fmt-check:
	@printf '%s\n' fmt-check >> "$$FIXTURE_LOG"
	@exit 17
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ciWriteExecutable(t, filepath.Join(bin, "golangci-lint"), `#!/bin/sh
set -eu
printf '%s\n' golangci-lint >> "$FIXTURE_LOG"
exit 0
`)
	ciWriteExecutable(t, filepath.Join(root, "run.sh"), `#!/usr/bin/env bash
exec "$FIXTURE_ROOT/scripts/ci/pr-lint.sh"
`)
	return ciFixture{root: root, script: filepath.Join(root, "run.sh"), log: log, summary: summary, bin: bin}
}

func runFixtureScript(t *testing.T, root, script string, values map[string]string) (string, int) {
	t.Helper()
	command := exec.Command("bash", script)
	command.Dir = root
	command.Env = ciFixtureEnvironment(values)
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(output), exitErr.ExitCode()
	}
	t.Fatalf("run fixture %s: %v", script, err)
	return string(output), -1
}

func ciFixtureEnvironment(values map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(values))
	keys := make(map[string]bool, len(values))
	for key := range values {
		keys[key] = true
	}
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !keys[key] {
			env = append(env, item)
		}
	}
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
}

func copyCISourceFile(t *testing.T, source, destination string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read fixture source %s: %v", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, mode); err != nil {
		t.Fatalf("write fixture file %s: %v", destination, err)
	}
}

func ciWriteExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func readMarkers(t *testing.T, path string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read marker log %s: %v", path, err)
	}
	markers := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			markers[line] = true
		}
	}
	return markers
}

func markerExists(t *testing.T, path, marker string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("read marker log %s: %v", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

func readCIFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture output %s: %v", path, err)
	}
	return string(data)
}

type embeddedStorageShardFixture struct {
	root         string
	runner       string
	sourceRunner string
	binary       string
	manifest     string
}

func newEmbeddedStorageShardFixture(t *testing.T) embeddedStorageShardFixture {
	t.Helper()
	root := t.TempDir()
	sourceRunner := filepath.Join(sourceRepoRoot(t), ".github", "scripts", "embedded-storage-test-shard.sh")
	runner := filepath.Join(root, "run-shard.sh")
	ciWriteExecutable(t, runner, fmt.Sprintf("#!/usr/bin/env bash\nexec %q 1 5 \"$@\"\n", sourceRunner))
	binary := filepath.Join(root, "embeddeddolt-test")
	ciWriteExecutable(t, binary, `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "-test.list" ]]; then
  case "${FIXTURE_MODE:-pass}" in
    list-fail) exit 41 ;;
    empty-list) exit 0 ;;
  esac
  printf '%s\n' TestAlpha TestBeta TestGamma TestDelta TestEpsilon TestNew TestConformance TestHelperProcess
  exit 0
fi
if [[ "${FIXTURE_MODE:-pass}" == "unparseable" ]]; then
  printf '%s\n' 'fixture produced no Go timing line'
  exit 0
fi
if [[ -n "${FIXTURE_ARGS:-}" ]]; then
  printf '%s\n' "$*" > "$FIXTURE_ARGS"
fi
printf '%s\n' '=== RUN   TestAlpha' '--- FAIL: TestAlpha (0.01s)'
if [[ "${FIXTURE_MODE:-pass}" == "fail" ]]; then
  exit 37
fi
exit 0
`)
	manifest := filepath.Join(root, "manifest.txt")
	if err := os.WriteFile(manifest, []byte("5 1 TestAlpha\n5 2 TestBeta\n5 3 TestGamma\n5 4 TestDelta\n5 5 TestEpsilon\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return embeddedStorageShardFixture{root: root, runner: runner, sourceRunner: sourceRunner, binary: binary, manifest: manifest}
}

func readStorageSelected(t *testing.T, path string) map[string]string {
	t.Helper()
	selected := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(readCIFile(t, path)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			t.Fatalf("selected artifact row %q, want test source shard", line)
		}
		selected[fields[0]] = fields[1]
	}
	return selected
}

func sortedStorageNames(assignments map[string]string) []string {
	names := make([]string, 0, len(assignments))
	for name := range assignments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
