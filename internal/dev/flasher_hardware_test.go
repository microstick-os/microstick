package dev

import (
	"bytes"
	"errors"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"testing"
)

func TestWizardUsesTargetCalibrationWithoutChangingDeveloperBundle(t *testing.T) {
	p := testPaths(t)
	dir := t.TempDir()
	original, e := hashFile(filepath.Join(p.Hardware, "MANIFEST.json"))
	if e != nil {
		t.Fatal(e)
	}
	modem := filepath.Join(dir, "modem.img")
	file, e := os.Create(modem)
	if e != nil {
		t.Fatal(e)
	}
	file.Truncate(64 << 20)
	file.Close()
	if _, e = command("", nil, "mformat", "-i", modem, "::"); e != nil {
		t.Fatal(e)
	}
	if _, e = command("", nil, "mmd", "-i", modem, "::/image"); e != nil {
		t.Fatal(e)
	}
	var dsp DSP
	if e = readJSON(filepath.Join(p.Hardware, "dsp-firmware.json"), &dsp); e != nil {
		t.Fatal(e)
	}
	for _, f := range dsp.Files {
		if f.Name == "mba.mbn" {
			continue
		}
		if _, e = command("", nil, "mcopy", "-i", modem, filepath.Join(p.Hardware, f.Source), "::/image/"+f.Name); e != nil {
			t.Fatal(e)
		}
	}
	persist := filepath.Join(dir, "persist.img")
	file, e = os.Create(persist)
	if e != nil {
		t.Fatal(e)
	}
	file.Truncate(8 << 20)
	file.Close()
	if _, e = command("", nil, "mke2fs", "-q", "-F", "-t", "ext4", "-E", "lazy_itable_init=0,lazy_journal_init=0", persist); e != nil {
		t.Fatal(e)
	}
	own := bytes.Repeat([]byte{0x53}, 31000)
	nv := filepath.Join(dir, "nv")
	if e = os.WriteFile(nv, own, 0600); e != nil {
		t.Fatal(e)
	}
	if e = imagePut(persist, "/WCNSS_qcom_wlan_nv.bin", nv, 0100600); e != nil {
		t.Fatal(e)
	}
	staged, e := stageWizardHardware(p, modem, persist, filepath.Join(dir, "target-hardware"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyOwnWizardInputs(staged, modem, persist, nil); e != nil {
		t.Fatal(e)
	}
	var fm FirmwareMap
	if e = readJSON(filepath.Join(staged.Hardware, "derived/firmware-map.json"), &fm); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, f := range fm.Files {
		if f.Component == "wifi-device-NV" {
			b, e := os.ReadFile(filepath.Join(staged.Hardware, f.Artifact))
			if e != nil || !bytes.Equal(b, own) {
				t.Fatal("target calibration not used", e)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing target calibration")
	}
	after, e := hashFile(filepath.Join(p.Hardware, "MANIFEST.json"))
	if e != nil || after != original {
		t.Fatal("canonical hardware changed", e)
	}
	for _, f := range dsp.Files {
		if f.Name != "mba.mbn" {
			bad := filepath.Join(dir, "unsupported-code")
			mustWrite(t, bad, "unsupported firmware fixture\n")
			if _, e = command("", nil, "mcopy", "-o", "-i", modem, bad, "::/image/"+f.Name); e != nil {
				t.Fatal(e)
			}
			_, e = stageWizardHardware(p, modem, persist, filepath.Join(dir, "unsupported-hardware"))
			if !errors.Is(e, flasher.ErrUnsupported) {
				t.Fatal("unsupported firmware did not request support archive", e)
			}
			break
		}
	}
}
