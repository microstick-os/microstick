package dev

import (
	"errors"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
)

// Stage the target's calibration/DSP around the common verified Linux bundle.
func stageWizardHardware(p Paths, modem, persist, root string) (Paths, error) {
	var m Manifest
	if e := readJSON(filepath.Join(p.Hardware, "MANIFEST.json"), &m); e != nil {
		return p, e
	}
	if e := verifyEntries(p.Hardware, m.Artifacts); e != nil {
		return p, e
	}
	if e := copyTree(p.Hardware, root); e != nil {
		return p, e
	}
	if e := os.RemoveAll(filepath.Join(root, "original")); e != nil {
		return p, e
	}
	for _, name := range []string{"boot/gpt-primary.bin", "boot/gpt-backup.bin", "boot/installed-layout-plan.json"} {
		if e := os.Remove(filepath.Join(root, name)); e != nil && !os.IsNotExist(e) {
			return p, e
		}
	}
	var dsp DSP
	if e := readJSON(filepath.Join(root, "dsp-firmware.json"), &dsp); e != nil {
		return p, e
	}
	var own []Entry
	for _, v := range dsp.Files {
		if v.Name == "mba.mbn" {
			own = append(own, v)
		}
	}
	for _, pattern := range []string{"modem.*", "wcnss.*"} {
		names, e := filepath.Glob(filepath.Join(root, "firmware/dsp", pattern))
		if e != nil {
			return p, e
		}
		for _, name := range names {
			if e = os.Remove(name); e != nil {
				return p, e
			}
		}
	}
	for _, family := range []struct {
		stem    string
		machine uint16
	}{{"modem", 164}, {"wcnss", 40}} {
		files, e := extractOwnPIL(modem, root, family.stem, family.machine)
		if e != nil {
			return p, &flasher.UnsupportedInput{Code: "own-dsp", Message: e.Error()}
		}
		own = append(own, files...)
	}
	dsp.Files = own
	// Enable the modem guard only for the firmware hash declared in its profile.
	guardMatches := false
	for _, f := range own {
		if f.Name == dsp.Guard["firmware_file"] && f.SHA256 == dsp.Guard["firmware_sha256"] {
			guardMatches = true
		}
	}
	if !guardMatches {
		dsp.Guard = nil
	}
	if e := writeJSON(filepath.Join(root, "dsp-firmware.json"), dsp, 0600); e != nil {
		return p, e
	}
	nv, e := ownWiFiCalibration(persist)
	if e != nil {
		return p, &flasher.UnsupportedInput{Code: "own-wifi-calibration", Message: "Не найдена собственная Wi-Fi калибровка в persist"}
	}
	var fm FirmwareMap
	if e = readJSON(filepath.Join(root, "derived/firmware-map.json"), &fm); e != nil {
		return p, e
	}
	found := false
	for _, f := range fm.Files {
		if f.Component != "wifi-device-NV" {
			continue
		}
		path, e := safeJoin(root, f.Artifact)
		if e != nil {
			return p, e
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return p, e
		}
		if e = os.WriteFile(path, nv, 0600); e != nil {
			return p, e
		}
		found = true
	}
	if !found {
		return p, errors.New("Wi-Fi calibration destination unavailable")
	}
	mapPath := filepath.Join(root, "derived/firmware-map.json")
	var mapping map[string]any
	if e = readJSON(mapPath, &mapping); e != nil {
		return p, e
	}
	if items, ok := mapping["files"].([]any); ok {
		for _, raw := range items {
			if item, ok := raw.(map[string]any); ok && item["component"] == "wifi-device-NV" {
				item["sha256"] = digest(nv)
				item["bytes"] = len(nv)
				item["device_specific"] = true
			}
		}
	}
	if e = writeJSON(mapPath, mapping, 0600); e != nil {
		return p, e
	}
	var profile Profile
	if e = readJSON(filepath.Join(root, "profile.json"), &profile); e != nil {
		return p, e
	}
	profile.DeviceID = digest(nv)[:8]
	if e = writeJSON(filepath.Join(root, "profile.json"), profile, 0600); e != nil {
		return p, e
	}
	if e = finalize(root, Manifest{Version: 2}); e != nil {
		return p, e
	}
	p.Hardware = root
	return p, nil
}
