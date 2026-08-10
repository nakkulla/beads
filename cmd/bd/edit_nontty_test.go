package main

import (
	"os"
	"strings"
	"testing"
)

func TestRequireInteractiveEditTerminalRejectsPipes(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinR.Close()
	defer stdinW.Close()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()

	err = requireInteractiveEditTerminal(stdinR, stdoutW)
	if err == nil {
		t.Fatal("non-TTY edit guard accepted pipe-backed stdin/stdout")
	}
	if !strings.Contains(err.Error(), "interactive terminal") || !strings.Contains(err.Error(), "bd update") || !strings.Contains(err.Error(), "--body-file") {
		t.Fatalf("guard error lacks non-interactive guidance: %v", err)
	}
}
