package dev

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceBundleDropsStaleReportsAndPreservesOnFailure(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "images")
	staged := filepath.Join(root, "staged")
	mustWrite(t, filepath.Join(dest, "verification.json"), `{"status":"PASS","old":true}`)
	mustWrite(t, filepath.Join(dest, "network-tests.txt"), "old test results")
	mustWrite(t, filepath.Join(staged, "boot.img"), "new boot")
	if e := replaceBundle(filepath.Join(root, "missing"), dest); e == nil {
		t.Fatal("missing staged bundle accepted")
	}
	if _, e := os.Stat(filepath.Join(dest, "verification.json")); e != nil {
		t.Fatal("failed publication lost preceding bundle")
	}
	if e := replaceBundle(staged, dest); e != nil {
		t.Fatal(e)
	}
	names, e := files(dest)
	if e != nil {
		t.Fatal(e)
	}
	if len(names) != 1 || names[0] != "boot.img" {
		t.Fatal("stale report published", names)
	}
}

func TestPrivatePathRejectsMissingLeafThroughRepositorySymlink(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0755)
	link := filepath.Join(root, "alias")
	if e := os.Symlink(repo, link); e != nil {
		t.Fatal(e)
	}
	if _, e := privatePath(repo, filepath.Join(link, "missing", "private.json")); e == nil {
		t.Fatal("private file could enter source through a symlink")
	}
}

func TestCLIHelpSucceedsWithoutDeviceOrImage(t *testing.T) {
	if e := ExtractCLI(Paths{}, []string{"--help"}); e != nil {
		t.Fatal(e)
	}
	if e := PersonalizeCLI(Paths{}, []string{"--help"}); e != nil {
		t.Fatal(e)
	}
	if e := Main([]string{"--help"}); e != nil {
		t.Fatal(e)
	}
}
