package fix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
)

// seedLifecycleRig reuses the port-drift fixture shape: the pin fixer targets
// exactly the rigs that check looks at, minus the drift requirement.
func seedLifecycleRig(t *testing.T, doltMode string, storedPort int, marker string) string {
	t.Helper()
	tmpDir := seedDriftRig(t, doltMode, storedPort, 0)
	if marker == "" {
		return tmpDir
	}
	beadsDir := filepath.Join(tmpDir, ".beads")
	cfg, err := configfile.Load(beadsDir)
	if err != nil || cfg == nil {
		t.Fatalf("load seeded config: %v", err)
	}
	cfg.DoltServerLifecycle = marker
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

func TestInspectExternalLifecyclePin_Applicability(t *testing.T) {
	cases := []struct {
		name           string
		doltMode       string
		storedPort     int
		marker         string
		wantApplicable bool
		wantSkipHas    string
	}{
		{
			name:           "server rig classified external by port only",
			doltMode:       configfile.DoltModeServer,
			storedPort:     13307,
			wantApplicable: true,
		},
		{
			name:        "marker already present is idempotent",
			doltMode:    configfile.DoltModeServer,
			storedPort:  13307,
			marker:      configfile.DoltServerLifecycleExternal,
			wantSkipHas: "already",
		},
		{
			name:        "no dolt_server_port means nothing to migrate",
			doltMode:    configfile.DoltModeServer,
			storedPort:  0,
			wantSkipHas: "dolt_server_port",
		},
		{
			name:        "embedded rig",
			doltMode:    configfile.DoltModeEmbedded,
			storedPort:  13307,
			wantSkipHas: "not server",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := seedLifecycleRig(t, tc.doltMode, tc.storedPort, tc.marker)
			report := InspectExternalLifecyclePin(filepath.Join(tmpDir, ".beads"))

			if report.Applicable != tc.wantApplicable {
				t.Fatalf("Applicable = %v (skip %q), want %v", report.Applicable, report.SkipReason, tc.wantApplicable)
			}
			if tc.wantSkipHas != "" && !strings.Contains(report.SkipReason, tc.wantSkipHas) {
				t.Errorf("SkipReason = %q, want it to mention %q", report.SkipReason, tc.wantSkipHas)
			}
		})
	}
}

func TestInspectExternalLifecyclePin_SkipsSharedServer(t *testing.T) {
	tmpDir := seedLifecycleRig(t, configfile.DoltModeServer, 13307, "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")

	report := InspectExternalLifecyclePin(filepath.Join(tmpDir, ".beads"))
	if report.Applicable {
		t.Fatal("Applicable = true in shared-server mode; the lifecycle verdict is not this rig's to pin")
	}
	if !strings.Contains(report.SkipReason, "shared-server") {
		t.Errorf("SkipReason = %q, want it to mention shared-server", report.SkipReason)
	}
}

// TestInspectExternalLifecyclePin_SkipsSharedServerConfiguredOnTarget is the
// foreign-target case: `bd doctor <other-repo> --fix` must read the target's own
// config.yaml, not the caller's process-wide view, before writing a marker into
// the target's git-tracked metadata.json.
func TestInspectExternalLifecyclePin_SkipsSharedServerConfiguredOnTarget(t *testing.T) {
	tmpDir := seedLifecycleRig(t, configfile.DoltModeServer, 13307, "")
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"),
		[]byte("dolt:\n  shared-server: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The process view is deliberately left alone: only the target says shared.
	if doltserver.IsSharedServerMode() {
		t.Fatal("precondition: the process view must not already report shared-server mode")
	}

	report := InspectExternalLifecyclePin(beadsDir)
	if report.Applicable {
		t.Fatal("Applicable = true for a target rig configured as shared-server")
	}
	if !strings.Contains(report.SkipReason, "shared-server") {
		t.Errorf("SkipReason = %q, want it to mention shared-server", report.SkipReason)
	}

	if err := ExternalLifecyclePin(tmpDir); err == nil {
		t.Error("ExternalLifecyclePin succeeded on a shared-server target")
	}
	if _, present := rawMetadata(t, tmpDir)["dolt_server_lifecycle"]; present {
		t.Error("dolt_server_lifecycle was written into a shared-server rig")
	}
}

func TestInspectExternalLifecyclePin_NoMetadata(t *testing.T) {
	report := InspectExternalLifecyclePin(t.TempDir())
	if report.Applicable {
		t.Fatal("Applicable = true with no metadata.json")
	}
	if !strings.Contains(report.SkipReason, "metadata.json") {
		t.Errorf("SkipReason = %q, want it to mention metadata.json", report.SkipReason)
	}
}

// Diagnostic-only findings: reported, never auto-fixed.
func TestInspectExternalLifecyclePin_Diagnostics(t *testing.T) {
	t.Run("unrecognized marker value", func(t *testing.T) {
		tmpDir := seedLifecycleRig(t, configfile.DoltModeServer, 13307, "extenral")
		report := InspectExternalLifecyclePin(filepath.Join(tmpDir, ".beads"))

		if report.Applicable {
			t.Error("Applicable = true; an unrecognized marker is already suppressing auto-start")
		}
		if report.UnrecognizedMarker != "extenral" {
			t.Errorf("UnrecognizedMarker = %q, want %q", report.UnrecognizedMarker, "extenral")
		}
	})

	t.Run("recognized marker is not flagged", func(t *testing.T) {
		tmpDir := seedLifecycleRig(t, configfile.DoltModeServer, 13307, "  External  ")
		report := InspectExternalLifecyclePin(filepath.Join(tmpDir, ".beads"))

		if report.UnrecognizedMarker != "" {
			t.Errorf("UnrecognizedMarker = %q; trim + case folding must recognize the value", report.UnrecognizedMarker)
		}
	})

	t.Run("embedded rig carrying a marker", func(t *testing.T) {
		tmpDir := seedLifecycleRig(t, configfile.DoltModeEmbedded, 0, configfile.DoltServerLifecycleExternal)
		report := InspectExternalLifecyclePin(filepath.Join(tmpDir, ".beads"))

		if !report.EmbeddedWithMarker {
			t.Error("EmbeddedWithMarker = false; embedded + lifecycle marker is a contradiction worth reporting")
		}
		if report.Applicable {
			t.Error("Applicable = true on an embedded rig")
		}
	})
}

func TestExternalLifecyclePin_WritesMarkerAndIsIdempotent(t *testing.T) {
	tmpDir := seedLifecycleRig(t, configfile.DoltModeServer, 13307, "")
	beadsDir := filepath.Join(tmpDir, ".beads")

	if before := doltserver.ResolveServerMode(beadsDir); before != doltserver.ServerModeExternal {
		t.Fatalf("precondition: mode = %v, want External (port-only classification)", before)
	}

	if err := ExternalLifecyclePin(tmpDir); err != nil {
		t.Fatalf("ExternalLifecyclePin: %v", err)
	}

	meta := rawMetadata(t, tmpDir)
	if got := meta["dolt_server_lifecycle"]; got != configfile.DoltServerLifecycleExternal {
		t.Errorf("dolt_server_lifecycle = %v, want %q", got, configfile.DoltServerLifecycleExternal)
	}
	// The pin adds a signal; it never touches the port key. Dropping that key is
	// the port-drift fixer's job on a later run.
	if got, present := meta["dolt_server_port"]; !present || int(got.(float64)) != 13307 {
		t.Errorf("dolt_server_port = %v, want it left at 13307", got)
	}
	if after := doltserver.ResolveServerMode(beadsDir); after != doltserver.ServerModeExternal {
		t.Errorf("mode after pin = %v, want External", after)
	}

	if err := ExternalLifecyclePin(tmpDir); err == nil {
		t.Error("second ExternalLifecyclePin succeeded; the fix must be idempotent (not applicable once pinned)")
	}
}
