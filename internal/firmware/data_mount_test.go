package firmware

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDataMountChecksAndFailure(t *testing.T) {
	r := t.TempDir()
	conf := filepath.Join(r, "uuid")
	mounts := filepath.Join(r, "mounts")
	bin := filepath.Join(r, "bin")
	script := filepath.Join(r, "data-check")
	write(t, mounts, "")
	write(t, filepath.Join(bin, "blkid"), "#!/bin/sh\nprintf '%s\\n' /dev/fixture-data\n")
	write(t, filepath.Join(bin, "mount"), "#!/bin/sh\nprintf '%s\\n' '/dev/fixture-data /data ext4 rw,nodev,nosuid 0 0' > '"+mounts+"'\n")
	s := read(t, overlay("usr/libexec/microstick-data"))
	s = strings.ReplaceAll(s, "/etc/microstick/data-partuuid", conf)
	s = strings.ReplaceAll(s, "/proc/mounts", mounts)
	write(t, script, s)
	en := map[string]string{"PATH": bin + ":" + os.Getenv("PATH")}
	ok(t, en, "", script, "check") // legacy rootfs data is preserved when unconfigured
	write(t, conf, "01234567-89ab-cdef-0123-456789abcdef\n")
	if _, e := run(t, en, "", script, "check"); e == nil {
		t.Fatal("unmounted data accepted")
	}
	ok(t, en, "", script, "start")
	ok(t, en, "", script, "check")
	for _, line := range []string{"/dev/wrong /data ext4 rw 0 0\n", "/dev/fixture-data /data ext4 ro 0 0\n", "/dev/fixture-data /data tmpfs rw 0 0\n"} {
		write(t, mounts, line)
		if _, e := run(t, en, "", script, "start"); e == nil {
			t.Fatal("mounted wrong data or readonly data accepted")
		}
		equal(t, read(t, mounts), line)
	}
	write(t, mounts, "")
	write(t, filepath.Join(bin, "mount"), "#!/bin/sh\nexit 1\n")
	if _, e := run(t, en, "", script, "start"); e == nil {
		t.Fatal("mount failure ignored")
	}
	equal(t, read(t, mounts), "")
	write(t, conf, "not-a-partuuid\n")
	if _, e := run(t, en, "", script, "check"); e == nil {
		t.Fatal("bad PARTUUID accepted")
	}
}
