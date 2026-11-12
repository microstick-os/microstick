package dev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"microstick.dev/tools/internal/flasher"
)

// PrepareDeviceWizard is the common TUI/offline path. The reader may be native
// EDL or disposable files; source comparison and exact image preparation are identical.
func PrepareDeviceWizard(ctx context.Context, p Paths, d flasher.Reader, r flasher.HardwareReport, profile flasher.HardwareProfile, record, images string, o WizardOptions) (flasher.ImageAttestation, error) {
	att := flasher.ImageAttestation{Version: flasher.Schema, Device: r.Device, Profile: profile.ID, Images: map[string]string{}, Storage: o.Storage}
	var firmwareErr error
	att.BootFirmware, firmwareErr = flasher.InspectBootFirmware(ctx, d, r, profile.BootFirmwarePolicy)
	if firmwareErr != nil {
		return att, firmwareErr
	}
	o.BootFirmware = att.BootFirmware
	dtb, e := os.ReadFile(filepath.Join(p.Hardware, "device-tree/boot-image.dtb"))
	if e != nil {
		return att, e
	}
	if flasher.HashBytes(dtb) != profile.Bundle["dtb"] {
		dtb, e = flasher.BuildOwnLED17(dtb)
		if e != nil {
			return att, e
		}
	}
	if flasher.HashBytes(dtb) != profile.Bundle["dtb"] {
		return att, errors.New("selected DTB differs from reviewed profile")
	}
	o.SelectedDTB = dtb
	for _, part := range r.GPT.Partitions {
		if part.Name == "system" || part.Name == "rootfs" {
			o.RootCapacity = part.Range.Length
		}
	}
	o.RequestBinding = flasher.Hash([]any{r.Device, profile.ID, profile.Bundle, o.Storage})
	private := filepath.Join(p.Work, "buildroot-private")
	if e = os.MkdirAll(private, 0700); e != nil {
		return att, e
	}
	temp, e := os.MkdirTemp(private, ".wizard-inputs-")
	if e != nil {
		return att, e
	}
	defer os.RemoveAll(temp)
	readPart := func(name string) (string, error) {
		for _, part := range r.GPT.Partitions {
			if part.Name == name {
				dest := filepath.Join(temp, name+".img")
				f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					return "", e
				}
				if snap, ok := d.(*flasher.SnapshotReader); ok {
					e = flasher.MaterializeSnapshot(ctx, snap, part.Range, f)
				} else {
					e = d.Read(ctx, part.Range, f)
				}

				ce := f.Close()
				if e == nil {
					e = ce
				}
				return dest, e
			}
		}
		return "", fmt.Errorf("%w: missing own partition: %s", flasher.ErrUnsupported, name)
	}
	modem, e := readPart("modem")
	if e != nil {
		return att, e
	}
	persist, e := readPart("persist")
	if e != nil {
		return att, e
	}
	p, e = stageWizardHardware(p, modem, persist, filepath.Join(temp, "hardware"))
	if e != nil {
		return att, e
	}
	att.Bundle, e = VerifyOwnWizardInputs(p, modem, persist, dtb)
	if e != nil {
		return att, e
	}
	if !flasher.CompatibleBundle(profile, att.Bundle) {
		return att, errors.New("exact kernel/DTB/modules/DSP set differs from profile")
	}
	if !o.Clean {
		if o.Storage != nil && o.Storage.Source == "microstick" {
			o.OldRootfs, e = readPart("system")
		} else if o.Storage == nil {
			o.OldRootfs, e = readPart("rootfs")
		}
		if e != nil {
			return att, e
		}
	}
	if o.Storage == nil && !o.Update && o.OldRootfs != "" {
		marker, e := imageBytes(o.OldRootfs, "/etc/passwd")
		if e != nil {
			return att, e
		}
		if !containsMicrostick(marker) {
			o.OldRootfs = ""
			o.Clean = true
		}
	}
	if o.Storage != nil && o.Storage.DataMode == "preserve" {
		o.OldData, e = readPart("userdata")
		if e != nil {
			return att, e
		}
	}
	for _, image := range []string{o.OldRootfs, o.OldData} {
		if image != "" {
			if e = cleanWizardFilesystem(ctx, p, image); e != nil {
				return att, e
			}
		}
	}
	if e = ctx.Err(); e != nil {
		return att, e
	}
	if o.Update {
		installed, e := InstalledFlasherRelease(o.OldRootfs)
		if e != nil {
			return att, e
		}
		if o.Release == nil {
			return att, errors.New("update release missing")
		}
		if e = CheckFlasherUpdate(installed, *o.Release); e != nil {
			return att, e
		}
	}
	if e = PrepareWizard(p, p.Artifacts, record, images, o); e != nil {
		return att, e
	}
	names := []string{"boot.img", "rootfs.ext4"}
	for _, pair := range att.BootFirmware.Files() {
		names = append(names, pair[1])
	}
	names = append(names, "Payload.json")
	for _, name := range names {
		att.Images[name], e = flasher.HashFile(filepath.Join(images, name))
		if e != nil {
			return att, e
		}
	}
	att.ReceiptSHA256, e = flasher.HashFile(filepath.Join(images, "Preparation.json"))
	if e != nil {
		return att, e
	}
	if e = ctx.Err(); e != nil {
		return att, e
	}
	return att, FinalizeWizardAttestation(images, att)
}
