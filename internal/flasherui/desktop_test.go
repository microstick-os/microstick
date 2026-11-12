package flasherui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDesktopActionsPreserveLiteralPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Папка с пробелами;$(literal)", "report.tar.gz")
	t.Setenv("PATH", root)
	t.Setenv("WAYLAND_DISPLAY", "test-wayland")
	t.Setenv("DISPLAY", "")
	capture := filepath.Join(root, "copy")
	t.Setenv("COPY_CAPTURE", capture)
	if e := os.WriteFile(filepath.Join(root, "wl-copy"), []byte("#!/bin/sh\n/bin/cat > \"$COPY_CAPTURE\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e := copyDesktopPath(ctx, path); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(capture)
	if e != nil || string(raw) != path {
		t.Fatal("clipboard changed path", e)
	}
	if e := os.WriteFile(filepath.Join(root, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$COPY_CAPTURE\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := openDesktopFolder(ctx, filepath.Dir(path)); e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile(capture)
	if e != nil || string(raw) != filepath.Dir(path) {
		t.Fatal("folder argument changed", e)
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	if openDesktopFolder(ctx, root) == nil {
		t.Fatal("headless opener reported success")
	}
}
