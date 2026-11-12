package main

import (
	"testing"
)

func TestHelpDoesNotRequireWorkspaceOrHardware(t *testing.T) {
	t.Setenv("MICROSTICK_WORKSPACE", "")
	if e := run([]string{"--help"}); e != nil {
		t.Fatal(e)
	}
}

func TestVersionDoesNotRequireWorkspaceOrHardware(t *testing.T) {
	t.Setenv("MICROSTICK_WORKSPACE", "")
	if e := run([]string{"--version"}); e != nil {
		t.Fatal(e)
	}
}
