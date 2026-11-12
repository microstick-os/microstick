package flasher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSecureProbeWire(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	t.Setenv("UBSAN_OPTIONS", "halt_on_error=1")
	flags := strings.Fields(string(runTest(t, "pkg-config", "--cflags", "--libs", "libxml-2.0")))
	out := filepath.Join(t.TempDir(), "secure-test")
	args := []string{"-O1", "-Wall", "-Wextra", "-Werror", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-fno-pie", "-no-pie", "../../scripts/native/edl-security-test.c", "-o", out}
	args = append(args, flags...)
	runTest(t, "cc", args...)
	runTest(t, out)
}

func TestEDLSecureBootProtocolRejectsUnknownEvidence(t *testing.T) {
	for _, response := range []string{"S disabled", "S enabled", "S unknown", "E boot-security", "S disabled trailing", "S 0x00 0x00 0x00 0x00"} {
		t.Run(response, func(t *testing.T) {
			helper := filepath.Join(t.TempDir(), "helper")
			writeTest(t, helper, []byte("#!/bin/sh\nprintf 'I2 firehose 00000000 0000000000000000 1048576 4096 4096\\n'\nread -r cmd\n[ \"$cmd\" = S ] || exit 3\nprintf '%s\\n' '"+response+"'\n"))
			if e := os.Chmod(helper, 0700); e != nil {
				t.Fatal(e)
			}
			d, e := OpenEDL(testCtx, helper, "unused", "")
			if e != nil {
				t.Fatal(e)
			}
			defer d.Close()
			got, e := d.SecureBoot(testCtx)
			if response == "S disabled" || response == "S enabled" {
				if e != nil || "S "+got != response {
					t.Fatal(got, e)
				}
			} else if e == nil || strings.Contains(e.Error(), "0x00") {
				t.Fatal("invalid/raw evidence accepted or leaked", got, e)
			}
		})
	}
}
