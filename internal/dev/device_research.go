package dev

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"microstick.dev/tools/internal/flasher"
	"microstick.dev/tools/internal/l10n"
)

// Research is bound to both the device and the measured source/storage layout.
// USB topology is used only to carry an unbound ADB capture into its first EDL session.
type deviceResearch struct {
	Version      int                      `json:"schema_version"`
	Device       flasher.DeviceInfo       `json:"device"`
	Storage      string                   `json:"storage_sha256"`
	Sources      string                   `json:"sources_sha256"`
	ActiveDTB    []byte                   `json:"active_dtb"`
	ActiveSHA256 string                   `json:"active_sha256"`
	Automatic    *flasher.AutomaticInputs `json:"automatic,omitempty"`
}

func normalizeUSBPath(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "usb:")
	value = strings.TrimPrefix(value, "@")
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return "@" + value
	}
	numbers := append([]string{parts[0]}, strings.Split(parts[1], ".")...)
	for i, v := range numbers {
		n, e := strconv.ParseUint(v, 10, 16)
		if e != nil {
			return "@" + value
		}
		numbers[i] = strconv.FormatUint(n, 10)
	}
	return "@" + numbers[0] + "-" + strings.Join(numbers[1:], ".")
}

func researchFile(p Paths, r flasher.HardwareReport) string {
	return filepath.Join(p.Work, "buildroot-private/device-research", flasher.Hash(r.Device)+".json")
}

func readResearchJSON(path string, value any) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return errors.New("invalid device research record")
	}
	return json.NewDecoder(io.LimitReader(f, 32<<20)).Decode(value)
}

func ResearchTree(p Paths, r flasher.HardwareReport, usbPath string, provided []byte, progress flasher.Emit) ([]byte, error) {
	var capture *LiveTree
	if usbPath != "" {
		var e error
		capture, e = LoadLiveTree(p, usbPath, r)
		if e != nil {
			return nil, e
		}
		// A fresh capture supersedes a previously bound record for this device.
		if len(provided) == 0 && capture != nil && capture.Device == nil {
			provided = capture.DTB
		}
	}
	var saved deviceResearch
	e := readResearchJSON(researchFile(p, r), &saved)
	if e == nil && (saved.Version != 1 || !flasher.EqualDevice(saved.Device, r.Device) || saved.ActiveSHA256 != flasher.HashBytes(saved.ActiveDTB)) {
		return nil, errors.New("saved device research integrity differs")
	}
	if e == nil && len(provided) == 0 && saved.Storage == flasher.StorageFingerprint(r) && saved.Sources == flasher.SourceSetFingerprint(r.Variants) {
		if e = flasher.ValidateActiveDTB(saved.ActiveDTB); e != nil {
			return nil, e
		}
		if e = flasher.BindActiveDTB(r, saved.ActiveDTB); e != nil {
			return nil, e
		}
		if progress != nil {
			progress(flasher.Progress{Stage: "research", Message: l10n.Text("ui.using_saved_research")})
		}
		return saved.ActiveDTB, nil
	}
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	active := provided
	if len(active) == 0 && usbPath != "" {
		if capture != nil {
			active = capture.DTB
		}
	}
	if len(active) == 0 {
		return nil, nil
	}
	if e = flasher.ValidateActiveDTB(active); e != nil {
		return nil, e
	}
	if e = flasher.BindActiveDTB(r, active); e != nil {
		return nil, e
	}
	if progress != nil {
		progress(flasher.Progress{Stage: "research", Message: l10n.Text("ui.binding_saved_research")})
	}
	if e = SaveDeviceResearch(p, r, active, nil); e != nil {
		return nil, e
	}
	if usbPath != "" {
		live := capture
		if live != nil && flasher.HashBytes(live.DTB) == flasher.HashBytes(active) {
			live.Device = &r.Device
			live.Sources = flasher.SourceSetFingerprint(r.Variants)
			if err := writeJSONAtomic(filepath.Join(p.Work, "buildroot-private/live-device-trees", digest([]byte(live.USBPath))+".json"), live); err != nil {
				return nil, err
			}
		}
	}
	return active, nil
}

func SaveDeviceResearch(p Paths, r flasher.HardwareReport, active []byte, automatic *flasher.AutomaticInputs) error {
	if len(active) == 0 {
		return nil
	}
	if e := flasher.ValidateActiveDTB(active); e != nil {
		return e
	}
	if e := flasher.BindActiveDTB(r, active); e != nil {
		return e
	}
	saved := deviceResearch{Version: 1, Device: r.Device, Storage: flasher.StorageFingerprint(r), Sources: flasher.SourceSetFingerprint(r.Variants), ActiveDTB: active, ActiveSHA256: flasher.HashBytes(active), Automatic: automatic}
	path := researchFile(p, r)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	return writeJSONAtomic(path, saved)
}
