package protocol

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEditRejectsNonTTYWithoutLaunchingEditor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sentinel shell script is POSIX-only")
	}
	w := newWorkspace(t)
	id := w.create("Non-TTY edit")
	marker := filepath.Join(t.TempDir(), "editor-called")
	editor := filepath.Join(t.TempDir(), "editor-sentinel")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\ntouch \"$BD_EDIT_SENTINEL\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(w.bd, "edit", id)
	cmd.Dir = w.dir
	cmd.Env = append(w.env(), "EDITOR="+editor, "BD_EDIT_SENTINEL="+marker)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("non-TTY edit succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "interactive terminal") || !strings.Contains(string(out), "bd update") {
		t.Fatalf("non-TTY error lacks guidance: %s", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("editor sentinel was invoked; marker stat error = %v", err)
	}
}
