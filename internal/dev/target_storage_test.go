package dev

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestActualTargetFilesystemToolsFormatCheckAndGrow(t *testing.T) {
	p := testPaths(t)
	image := filepath.Join(t.TempDir(), "target.ext4")
	file, e := os.Create(image)
	if e != nil {
		t.Fatal(e)
	}
	file.Truncate(16 << 20)
	file.Close()
	qemu := filepath.Join(p.Host, "bin/qemu-aarch64")
	invoke := func(name string, args ...string) {
		t.Helper()
		argv := []string{"-L", p.Target, "-E", "LD_LIBRARY_PATH=" + filepath.Join(p.Target, "lib") + ":" + filepath.Join(p.Target, "usr/lib"), filepath.Join(p.Target, "sbin", name)}
		argv = append(argv, args...)
		if out, e := exec.Command(qemu, argv...).CombinedOutput(); e != nil {
			t.Fatalf("target %s: %v %s", name, e, out)
		}
	}
	invoke("mke2fs", "-q", "-F", "-t", "ext4", "-b", "4096", "-O", "^64bit,^metadata_csum_seed,^orphan_file", image)
	invoke("e2fsck", "-fn", image)
	invoke("resize2fs", image, "32M")
	invoke("e2fsck", "-fn", image)
	st, e := os.Stat(image)
	if e != nil || st.Size() != 32<<20 {
		t.Fatal("target grow", e)
	}
	// Early tools really exist inside the generated compressed initramfs, not only
	// in the rootfs where they would be unavailable before mounting it.
	ram, e := os.ReadFile(filepath.Join(p.Artifacts, "initramfs.cpio.gz"))
	if e != nil {
		t.Fatal(e)
	}
	gz, e := gzip.NewReader(bytes.NewReader(ram))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(gz)
	gz.Close()
	if e != nil {
		t.Fatal(e)
	}
	names, e := cpioNames(raw)
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(names, "\n")
	for _, name := range []string{"sbin/resize2fs", "sbin/e2fsck", "sbin/dumpe2fs"} {
		if !strings.Contains(joined, name) {
			t.Fatal("early tool missing", name)
		}
	}
}
