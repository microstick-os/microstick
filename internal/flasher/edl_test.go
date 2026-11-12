package flasher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEDLReconnectOnlyForTransportErrors(t *testing.T) {
	for _, code := range []string{"device-not-found", "session-detect", "sahara-identity", "programmer-upload", "source-read", "write-transfer", "readback-read"} {
		if !RetryableEDL(fmt.Errorf("operation: %w", edlStageError(code))) {
			t.Fatal("transport not retryable", code)
		}
	}
	for _, code := range []string{"USB_PERMISSION_DENIED", "multiple-devices", "geometry-user", "source-changed", "input-hash", "readback-hash"} {
		if RetryableEDL(edlStageError(code)) {
			t.Fatal("integrity/permission retry", code)
		}
	}
	if !RetryableEDL(fmt.Errorf("write: %w", ErrEDLTransport)) || RetryableEDL(errors.New("unrelated")) {
		t.Fatal("transport classification")
	}
}

func TestEDLBridgeProtocolHasNoWriteOrReset(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	log := filepath.Join(dir, "commands")
	t.Setenv("MICROSTICK_BRIDGE_TEST_LOG", log)
	script := `#!/bin/sh
printf 'I 00000001 0000000000000002 1048576 4096 4096\n'
while read -r op area off len; do
  printf '%s\n' "$op" >> "$MICROSTICK_BRIDGE_TEST_LOG"
  case "$op" in
    I) printf 'I 00000001 0000000000000002 1048576 4096 4096\n';;
    R) head -c "$len" /dev/zero;;
    *) exit 3;;
  esac
done
`
	if e := os.WriteFile(helper, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	d, e := OpenEDL(context.Background(), helper, "unused-programmer", "connection-only")
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	if _, ok := any(d).(Writer); ok {
		t.Fatal("initial native session exposes Writer")
	}
	info, e := d.Info(testCtx)
	if e != nil || info.Identity.Kind != "sahara" || info.Identity.Hardware == "connection-only" {
		t.Fatal("identity did not come from Sahara", e)
	}
	var out bytes.Buffer
	if e = d.Read(testCtx, Range{"user", 512, 32768}, &out); e != nil || out.Len() != 32768 {
		t.Fatal("bounded read failed", e)
	}
	for _, r := range []Range{{"user", 1, 512}, {"rpmb", 0, 512}, {"boot1", 4096, 512}} {
		if e = d.Read(testCtx, r, &out); e == nil {
			t.Fatal("invalid read reached bridge")
		}
	}
	b, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Fields(string(b)) {
		if line != "I" && line != "R" {
			t.Fatal("write/reset/provisioning command reached readonly bridge")
		}
	}
}
func TestConflictAutomaticallyExportsBoardReport(t *testing.T) {
	f := newFixture(t)
	raw := makeDT(t, `other { gpios=<&gpio 71 0>; };`)
	r := f.report.GPT.Partitions[0].Range
	payload := make([]byte, r.Length)
	copy(payload, raw)
	if e := f.d.Write(testCtx, r, bytes.NewReader(payload)); e != nil {
		t.Fatal(e)
	}
	result, e := InspectAndReport(testCtx, f.d, nil, filepath.Join(f.root, "reports"), nil)
	if e != nil || result.Report.Resolution.Status != "conflict" || result.Archive == "" {
		t.Fatal("conflicting profile did not generate automatic archive", e)
	}
}

func TestEDLStartupErrorsIdentifyStageWithoutLeakingNativeOutput(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{"USB_PERMISSION_DENIED", "ACCESS/-3"}, {"device-not-found", "05c6:9008"}, {"multiple-devices", "--edl-serial"},
		{"usb-init", "инициализировать USB"}, {"usb-open", "права доступа"},
		{"sahara-identity", "Sahara"}, {"programmer-file", "локальный файл"},
		{"programmer-upload", "RAM"}, {"firehose-configure", "Firehose"},
		{"geometry-user", "(user)"}, {"geometry-boot1", "boot1"}, {"geometry-boot2", "boot2"},
		{"PRIVATE-HARDWARE-SENTINEL", "неизвестный этап"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			helper := filepath.Join(t.TempDir(), "helper")
			script := "#!/bin/sh\nprintf 'PRIVATE-STDERR-SENTINEL\\n' >&2\nprintf 'E " + tc.code + "\\n'\nexit 3\n"
			if e := os.WriteFile(helper, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			_, e := OpenEDL(testCtx, helper, "unused", "")
			if e == nil {
				t.Fatal("helper failure accepted")
			}
			if !strings.Contains(e.Error(), tc.want) || !strings.Contains(e.Error(), "Запись не начата") {
				t.Fatal("startup stage not explained", e)
			}
			if strings.Contains(e.Error(), "PRIVATE-") {
				t.Fatal("private native output leaked")
			}
		})
	}
}
