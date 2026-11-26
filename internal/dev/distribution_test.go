package dev

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"microstick.dev/tools/internal/flasher"
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

func TestAutomaticProfilePreparesCompleteInstallationWithoutCatalog(t *testing.T) {
	p := testPaths(t)
	root := t.TempDir()
	p.Work = filepath.Join(root, "state")
	active := filepath.Join(root, "active.dtb")
	source := filepath.Join(root, "active.dts")
	// Component bindings are synthetic and independent of a connected board.
	mustWrite(t, source, `/dts-v1/; / {compatible="synthetic,automatic","qcom,msm8916"; #address-cells=<1>;#size-cells=<1>;
 memory {device_type="memory";reg=<0x80000000 0x20000000>;};
 regulators {compatible="qcom,rpm-pm8916-regulators";
 vdd:l8 {phandle=<31>;regulator-name="8916_l8";regulator-min-microvolt=<2850000>;regulator-max-microvolt=<2950000>;};
 vio:l5 {phandle=<32>;regulator-name="8916_l5";regulator-min-microvolt=<1800000>;regulator-max-microvolt=<1800000>;};};
 gpio:pinctrl {compatible="qcom,msm-gpio";gpio-controller;#gpio-cells=<2>;phandle=<17>;};
 usb {compatible="qcom,hsusb-otg";reg=<0x078d9000 0x400>;};
 emmc {compatible="qcom,sdhci-msm";reg=<0x07824000 0x800>;bus-width=<8>;vdd-supply=<&vdd>;vdd-io-supply=<&vio>;};
 wifi {compatible="qcom,wcnss_wlan";};modem {compatible="qcom,pil-q6v56-mss";};
 spmi {compatible="qcom,spmi-pmic-arb";reg=<0x0200f000 0x1000>;pmic {compatible="qcom,pm8916";};};
 leds {compatible="gpio-leds";light {label="activity";gpios=<&gpio 79 0>;default-state="off";};};
 keys {compatible="gpio-keys";reset {gpios=<&gpio 37 1>;linux,code=<0x198>;};};};`)
	if _, e := command("", nil, "dtc", "-q", "-I", "dts", "-O", "dtb", "-o", active, source); e != nil {
		t.Fatal(e)
	}
	tree, e := os.ReadFile(active)
	if e != nil {
		t.Fatal(e)
	}
	paths := map[string]string{}
	user := filepath.Join(root, "user.bin")
	file, e := os.Create(user)
	if e != nil {
		t.Fatal(e)
	}
	file.Truncate(512 << 20)
	file.Close()
	paths["user"] = user
	for _, area := range []string{"boot1", "boot2"} {
		path := filepath.Join(root, area)
		mustWrite(t, path, string(make([]byte, 4096)))
		paths[area] = path
	}
	device, e := flasher.OpenFiles(paths, "automatic-fixture", false)
	if e != nil {
		t.Fatal(e)
	}
	defer device.Close()
	info, e := device.Info(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	r := flasher.HardwareReport{Version: flasher.Schema, Device: info, ActiveDTB: tree, ActiveOrigin: "live"}
	parts := []flasher.Partition{
		{Name: "boot", GUID: "01234567-89ab-cdef-0123-456789abcdef", Range: flasher.Range{Area: "user", Offset: 1 << 20, Length: 16 << 20}},
		{Name: "rootfs", Range: flasher.Range{Area: "user", Offset: 17 << 20, Length: 288 << 20}},
		{Name: "modem", Range: flasher.Range{Area: "user", Offset: 305 << 20, Length: 64 << 20}},
		{Name: "persist", Range: flasher.Range{Area: "user", Offset: 369 << 20, Length: 8 << 20}},
		{Name: "tz", Range: flasher.Range{Area: "user", Offset: 377 << 20, Length: 512 << 10}},
		{Name: "hyp", Range: flasher.Range{Area: "user", Offset: 378 << 20, Length: 128 << 10}},
	}
	for i := range parts {
		if parts[i].GUID == "" {
			parts[i].GUID = fmt.Sprintf("01234567-89ab-cdef-0123-%012d", i+1)
		}
	}
	r.GPT.Partitions = parts
	v, e := flasher.ParseDTB(tree, "boot", 0)
	if e != nil {
		t.Fatal(e)
	}
	r.Variants = []flasher.DTVariant{v}
	profile, e := DetectAutomaticProfile(p, r)
	if e != nil {
		t.Fatal("common runtime could not derive profile", e)
	}
	r.Resolution = flasher.Resolve(r, []flasher.HardwareProfile{profile})
	if r.Resolution.Status != "matched" {
		t.Fatal(r.Resolution)
	}
	modem := filepath.Join(root, "modem.img")
	file, e = os.Create(modem)
	if e != nil {
		t.Fatal(e)
	}
	file.Truncate(64 << 20)
	file.Close()
	for _, args := range [][]string{{"mformat", "-i", modem, "::"}, {"mmd", "-i", modem, "::/image"}} {
		if _, e = command("", nil, args[0], args[1:]...); e != nil {
			t.Fatal(e)
		}
	}
	var dsp DSP
	if e = readJSON(filepath.Join(p.Hardware, "dsp-firmware.json"), &dsp); e != nil {
		t.Fatal(e)
	}
	for _, f := range dsp.Files {
		if f.Name != "mba.mbn" {
			if _, e = command("", nil, "mcopy", "-i", modem, filepath.Join(p.Hardware, f.Source), "::/image/"+f.Name); e != nil {
				t.Fatal(e)
			}
		}
	}
	alternatePIL(t, modem, root)
	persist := filepath.Join(root, "persist.img")
	file, e = os.Create(persist)
	if e != nil {
		t.Fatal(e)
	}
	file.Truncate(8 << 20)
	file.Close()
	if _, e = command("", nil, "mke2fs", "-q", "-F", "-t", "ext4", "-E", "lazy_itable_init=0,lazy_journal_init=0", persist); e != nil {
		t.Fatal(e)
	}
	calibration := filepath.Join(root, "calibration")
	own := bytes.Repeat([]byte{0x63}, 31000)
	if e = os.WriteFile(calibration, own, 0600); e != nil {
		t.Fatal(e)
	}
	if e = imagePut(persist, "/WCNSS_qcom_wlan_nv.bin", calibration, 0100600); e != nil {
		t.Fatal(e)
	}
	file, e = os.OpenFile(user, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	for name, path := range map[string]string{"modem": modem, "persist": persist} {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		for _, part := range parts {
			if part.Name == name {
				if _, e = file.WriteAt(b, int64(part.Range.Offset)); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	file.Close()
	// The secure-boot interface and exact TZ/HYP bytes are supplied by a synthetic reader.
	secure := automaticPreparationReader{Reader: device, parts: parts}
	output := filepath.Join(root, "prepared")
	record := filepath.Join(root, "installation.json")
	o := WizardOptions{Clean: true, SSID: "Synthetic-Automatic", APPassword: "synthetic-ap-password", RootPassword: "synthetic-root-password", RootPARTUUID: parts[0].GUID}
	att, e := PrepareDeviceWizard(context.Background(), p, secure, r, profile, record, output, o)
	if e != nil {
		t.Fatal("automatic installation preparation", e)
	}
	if att.Registry != nil || !flasher.CompatibleBundle(profile, att.Bundle) || att.BootFirmware == nil {
		t.Fatal("automatic attestation incomplete")
	}
	for _, name := range []string{"boot.img", "rootfs.ext4", "Payload.json"} {
		if att.Images[name] == "" {
			t.Fatal("prepared input missing", name)
		}
	}
	got, e := imageBytes(filepath.Join(output, "rootfs.ext4"), "/lib/firmware/wlan/prima/WCNSS_qcom_wlan_nv.bin")
	if e != nil || !bytes.Equal(got, own) {
		t.Fatal("own calibration not prepared", e)
	}
	op := NewFlasherOperation(p, r, FlasherRelease{}, "install")
	op.Automatic = profile.AutomaticInputs()
	if e = os.MkdirAll(filepath.Dir(op.Path), 0700); e != nil {
		t.Fatal(e)
	}
	if e = SaveFlasherOperation(op); e != nil {
		t.Fatal(e)
	}
	var saved FlasherOperation
	if e = readJSON(op.Path, &saved); e != nil {
		t.Fatal(e)
	}
	rebuilt, e := OperationProfile(&saved, nil, flasher.EngineVersion)
	if e != nil || flasher.Hash(rebuilt) != flasher.Hash(profile) {
		t.Fatal("automatic operation requires catalog or loses inputs", e)
	}
}

type automaticPreparationReader struct {
	flasher.Reader
	parts []flasher.Partition
}

func (automaticPreparationReader) SecureBoot(context.Context) (string, error) { return "disabled", nil }
func (d automaticPreparationReader) Read(ctx context.Context, r flasher.Range, w io.Writer) error {
	for _, p := range d.parts {
		if p.Range == r && (p.Name == "tz" || p.Name == "hyp") {
			b := make([]byte, r.Length)
			copy(b, "\x7fELF\x02\x01\x01")
			le := binary.LittleEndian
			le.PutUint16(b[16:], 2)
			le.PutUint16(b[18:], 183)
			le.PutUint32(b[20:], 1)
			le.PutUint64(b[32:], 64)
			le.PutUint16(b[52:], 64)
			le.PutUint16(b[54:], 56)
			le.PutUint16(b[56:], 1)
			base := uint64(0x86500000)
			if p.Name == "hyp" {
				base = 0x86400000
			}
			le.PutUint64(b[24:], base)
			le.PutUint32(b[64:], 1)
			le.PutUint64(b[72:], 512)
			le.PutUint64(b[80:], base)
			le.PutUint64(b[88:], base)
			le.PutUint64(b[96:], 512)
			le.PutUint64(b[104:], 1024)
			le.PutUint64(b[112:], 512)
			if p.Name == "tz" {
				copy(b[512:], "TZ.BF.3.0-synthetic")
			}
			_, e := w.Write(b)
			return e
		}
	}
	return d.Reader.Read(ctx, r, w)
}
