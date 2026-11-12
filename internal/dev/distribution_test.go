package dev

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleaseRootfsRemovesPrivateBytesAndRetainsMetadata(t *testing.T) {
	dir := t.TempDir()
	source, output := filepath.Join(dir, "private.tar"), filepath.Join(dir, "public.tar")
	f, e := os.Create(source)
	if e != nil {
		t.Fatal(e)
	}
	tw := tar.NewWriter(f)
	content := map[string]string{
		"etc/shadow":                  "root:$6$private-marker:0:0:99999:7:::\n",
		"etc/microstick/hostapd.conf": "private-marker",
		"lib/firmware/wlan/prima/WCNSS_qcom_wlan_nv.bin":             "private-marker",
		"usr/share/microstick/default-plugins/web/default-auth.json": "private-marker",
		"usr/share/microstick/hardware/build-provenance.json":        "private-marker",
		"usr/share/microstick/build.json":                            `{"buildroot":"2025.02.19","device_only":"private-marker","device_id":"private-marker"}`,
		"data/plugins/web/.state/auth.json":                          "private-marker",
		"etc/dropbear/dropbear_ed25519_host_key":                     "private-marker",
		"etc/machine-id":                                             "private-marker",
		"lib/firmware/modem.b00":                                     "private-marker",
		"lib/firmware/wcnss.mdt":                                     "private-marker",
		"lib/firmware/mba.mbn":                                       "common-code",
		"usr/bin/public":                                             "public-code",
	}
	for name, body := range content {
		if e = tw.WriteHeader(&tar.Header{Name: "./" + name, Size: int64(len(body)), Mode: 0755, Uid: 405, Gid: 405}); e != nil {
			t.Fatal(e)
		}
		if _, e = tw.Write([]byte(body)); e != nil {
			t.Fatal(e)
		}
	}
	if e = tw.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if e = publicRootfsTar(Paths{}, source, output); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(output)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(b, []byte("private-marker")) {
		t.Fatal("private bytes remain in release archive")
	}
	tr := tar.NewReader(bytes.NewReader(b))
	seen := map[string]string{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		seen[strings.TrimPrefix(h.Name, "./")] = string(body)
		if h.Uid != 405 || h.Gid != 405 || h.Mode != 0755 {
			t.Fatal("ownership or mode changed")
		}
	}
	if len(seen) != 5 || !strings.Contains(seen["usr/share/microstick/build.json"], "2025.02.19") || seen["usr/bin/public"] != "public-code" || seen["lib/firmware/mba.mbn"] != "common-code" || seen["etc/shadow"] != "root:!:0:0:99999:7:::\n" {
		t.Fatal("sanitized tree differs", seen)
	}
}

func TestPackagedHelpersNeedNoCompilerAndRejectTampering(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "bundle")
	t.Setenv("MICROSTICK_WORKSPACE", filepath.Join(dir, "state"))
	for _, name := range []string{"images/boot.img", "images/rootfs.ext4", "hardware/common", "tools/bin/extmap", "tools/bin/edl-read", "resources/programmer.mbn"} {
		mustWrite(t, filepath.Join(root, name), "release-file")
	}
	if e := finalize(filepath.Join(root, "images"), Manifest{Version: 2}); e != nil {
		t.Fatal(e)
	}
	if e := finalize(filepath.Join(root, "hardware"), Manifest{Version: 2}); e != nil {
		t.Fatal(e)
	}
	names, e := files(root)
	if e != nil {
		t.Fatal(e)
	}
	d := Distribution{Version: "1.0.0", Platform: runtime.GOOS + "/" + runtime.GOARCH}
	for _, name := range names {
		v, e := entry(root, name)
		if e != nil {
			t.Fatal(e)
		}
		d.Files = append(d.Files, v)
	}
	if e = writeJSON(filepath.Join(root, "DISTRIBUTION.json"), d, 0644); e != nil {
		t.Fatal(e)
	}
	p, e := OpenDistribution(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", "")
	mapper, bridge, programmer, e := FlasherBootstrap(p, true)
	if e != nil || mapper != filepath.Join(root, "tools/bin/extmap") || bridge != filepath.Join(root, "tools/bin/edl-read") || programmer != filepath.Join(root, "resources/programmer.mbn") {
		t.Fatal("packaged helpers unavailable", e)
	}
	mustWrite(t, mapper, "modified")
	if _, e = OpenDistribution(root); e == nil {
		t.Fatal("modified release accepted")
	}
}

func TestReleaseTemplatePreparesOwnFirmware(t *testing.T) {
	p := testPaths(t)
	fixture := t.TempDir()
	modem := filepath.Join(fixture, "modem.img")
	f, e := os.Create(modem)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(64 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	for _, args := range [][]string{{"mformat", "-i", modem, "::"}, {"mmd", "-i", modem, "::/image"}} {
		if _, e = command("", nil, args[0], args[1:]...); e != nil {
			t.Fatal(e)
		}
	}
	var dsp DSP
	if e = readJSON(filepath.Join(p.Hardware, "dsp-firmware.json"), &dsp); e != nil {
		t.Fatal(e)
	}
	for _, v := range dsp.Files {
		if v.Name != "mba.mbn" {
			if _, e = command("", nil, "mcopy", "-i", modem, filepath.Join(p.Hardware, v.Source), "::/image/"+v.Name); e != nil {
				t.Fatal(e)
			}
		}
	}
	persist := filepath.Join(fixture, "persist.img")
	f, e = os.Create(persist)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(8 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e = command("", nil, "mke2fs", "-q", "-F", "-t", "ext4", "-E", "lazy_itable_init=0,lazy_journal_init=0", persist); e != nil {
		t.Fatal(e)
	}
	own := bytes.Repeat([]byte{0x67}, 31000)
	calibration := filepath.Join(fixture, "own-nv")
	if e = os.WriteFile(calibration, own, 0600); e != nil {
		t.Fatal(e)
	}
	if e = imagePut(persist, "/WCNSS_qcom_wlan_nv.bin", calibration, 0100600); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(fixture, "distribution")
	if e = publicHardware(p, filepath.Join(root, "hardware")); e != nil {
		t.Fatal(e)
	}
	// Release preparation must not depend on preserving empty directories.
	if e = os.Remove(filepath.Join(root, "hardware/firmware/wifi")); e != nil {
		t.Fatal(e)
	}
	if e = publicImages(p, filepath.Join(root, "images"), "1.0.0"); e != nil {
		t.Fatal(e)
	}
	mapper, _, _, e := FlasherBootstrap(p, false)
	if e != nil {
		t.Fatal(e)
	}
	if e = copyFile(mapper, filepath.Join(root, "tools/bin/extmap"), 0755); e != nil {
		t.Fatal(e)
	}
	p.Work = filepath.Join(fixture, "state")
	p.Hardware, p.Artifacts, p.Distribution = filepath.Join(root, "hardware"), filepath.Join(root, "images"), root
	if imageExists(filepath.Join(p.Artifacts, "rootfs.ext4"), "/lib/firmware/wlan/prima/WCNSS_qcom_wlan_nv.bin") {
		t.Fatal("release contains calibration")
	}
	staged, e := stageWizardHardware(p, modem, persist, filepath.Join(fixture, "own-hardware"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyOwnWizardInputs(staged, modem, persist, nil); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(fixture, "prepared")
	options := WizardOptions{Clean: true, SSID: "Release-Example", APPassword: "example-ap-password", RootPassword: "example-root-password", RootPARTUUID: "12345678-1234-1234-1234-123456789abc"}
	if e = PrepareWizard(staged, p.Artifacts, filepath.Join(p.Work, "installation.json"), output, options); e != nil {
		t.Fatal(e)
	}
	if _, e = verifyManifest(output); e != nil {
		t.Fatal(e)
	}
	got, e := imageBytes(filepath.Join(output, "rootfs.ext4"), "/lib/firmware/wlan/prima/WCNSS_qcom_wlan_nv.bin")
	if e != nil || !bytes.Equal(got, own) {
		t.Fatal("own calibration was not injected", e)
	}
	shadow, e := imageBytes(filepath.Join(output, "rootfs.ext4"), "/etc/shadow")
	if e != nil || !bytes.Contains(shadow, []byte("root:$6$")) {
		t.Fatal("installation password was not set", e)
	}
	if _, e = os.Stat(filepath.Join(output, "data.ext4")); !os.IsNotExist(e) {
		t.Fatal("release preparation created a userdata image")
	}
}
