package dev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedFirehoseTransport(t *testing.T) {
	p := testPaths(t)
	// Never mutate a cached source or depend on its previous patches.
	stage := t.TempDir()
	archive := filepath.Join(p.Work, "buildroot-downloads/qdl-"+qdlRevision+".tar.gz")
	if _, e := command("", nil, "tar", "-xf", archive, "-C", stage); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(stage, "qdl-"+qdlRevision)
	if e := patchFlasherQDL(p, source); e != nil {
		t.Fatal(e)
	}
	flags, e := command("", nil, "pkg-config", "--cflags", "--libs", "libusb-1.0", "libxml-2.0")
	if e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(stage, "protocol-test")
	args := []string{"-O1", "-g", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-fno-pie", "-no-pie", "-ffunction-sections", "-fdata-sections", "-Wl,--gc-sections", "-I" + source, "-I" + filepath.Join(source, "src"), "-I" + filepath.Join(source, "include"), "-I" + filepath.Join(p.Repo, "scripts/native"), filepath.Join(p.Repo, "scripts/native/edl-protocol-test.c"), filepath.Join(source, "src/json.c"), filepath.Join(source, "src/ux.c"), "-o", out}
	args = append(args, strings.Fields(string(flags))...)
	if b, e := command("", nil, "cc", args...); e != nil {
		t.Fatal(string(b), e)
	}
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	t.Setenv("UBSAN_OPTIONS", "halt_on_error=1")
	for _, lun := range []string{"0", "1", "2"} {
		fixture := filepath.Join(p.Repo, "internal/dev/testdata/firehose/getstorageinfo-lun"+lun+".xml")
		if _, e = os.Stat(fixture); e != nil {
			t.Fatal(e)
		}
		if b, e := command("", nil, out, fixture, lun); e != nil {
			t.Fatal(string(b), e)
		}
	}
	if b, e := command("", nil, out); e != nil {
		t.Fatal(string(b), e)
	}
}
