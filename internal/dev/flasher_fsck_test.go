package dev

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoverDirtyStagedFilesystemWithoutChangingBackup(t *testing.T) {
	dir := t.TempDir()
	original := testExt4(t, dir, "saved-backup.ext4")
	for _, request := range []string{"set_super_value state 0", "feature needs_recovery"} {
		if _, e := debugImage(original, request, true); e != nil {
			t.Fatal(e)
		}
	}
	before, e := hashFile(original)
	if e != nil {
		t.Fatal(e)
	}
	p := Paths{Work: dir}
	staged := filepath.Join(dir, "buildroot-private/.wizard-inputs/rootfs.ext4")
	if e = copyFile(original, staged, 0600); e != nil {
		t.Fatal(e)
	}
	if e = cleanWizardFilesystem(context.Background(), p, staged); e != nil {
		t.Fatal(e)
	}
	after, e := hashFile(original)
	if e != nil || before != after {
		t.Fatal("saved backup was modified", e)
	}
	if _, e = command("", nil, "e2fsck", "-fn", staged); e != nil {
		t.Fatal("staged journal not recovered", e)
	}
	if e = cleanWizardFilesystem(context.Background(), p, original); e == nil {
		t.Fatal("original backup accepted for repair")
	}
	link := filepath.Join(dir, "buildroot-private/unsafe-link")
	if e = os.Symlink(original, link); e != nil {
		t.Fatal(e)
	}
	if e = cleanWizardFilesystem(context.Background(), p, link); e == nil {
		t.Fatal("symlink accepted for repair")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if e = cleanWizardFilesystem(cancelled, p, staged); e != context.Canceled {
		t.Fatal("cancelled recovery did not stop", e)
	}
}
