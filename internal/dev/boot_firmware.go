package dev

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"microstick.dev/tools/internal/flasher"
)

const bootTZSourceSHA256 = "2daaedaafeee5697b6d85aa329cc6af9bc7e661677e4594d75efb54e4b53fea1"

func stageBootFirmware(p Paths, bundle string, f *flasher.BootFirmware) error {
	if f == nil || !f.Upgrade {
		return nil
	}
	if f.Policy != flasher.BootFirmwarePolicy || f.Security != "disabled" {
		return errors.New("boot firmware upgrade lacks hardware authorization")
	}
	if p.Distribution != "" {
		for _, v := range []struct {
			name, hash string
			capacity   uint64
		}{{flasher.BootTZFile, flasher.BootTZSHA256, f.TZ.Range.Length}, {flasher.BootHYPFile, flasher.BootHYPSHA256, f.HYP.Range.Length}} {
			b, e := os.ReadFile(filepath.Join(p.Distribution, "resources", v.name))
			if e != nil {
				return e
			}
			if digest(b) != v.hash || uint64(len(b)) > v.capacity {
				return errors.New("release boot firmware does not fit verified recipe")
			}
			if e = os.WriteFile(filepath.Join(bundle, v.name), b, 0600); e != nil {
				return e
			}
		}
		return nil
	}
	archive, _, e := resourceFile(p, "boot-firmware")
	if e != nil {
		return e
	}
	z, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	defer z.Close()
	// Deliberately select only executable boot code. The upstream archive also
	// contains GPT and NV; none of those files is extracted or installed.
	wanted := map[string]string{"base/tz.mbn": flasher.BootTZFile, "base/hyp.mbn": flasher.BootHYPFile}
	found := 0
	for _, entry := range z.File {
		name, ok := wanted[entry.Name]
		if !ok {
			continue
		}
		if entry.UncompressedSize64 > 2<<20 {
			return errors.New("boot firmware member bound")
		}
		reader, e := entry.Open()
		if e != nil {
			return e
		}
		b, e := io.ReadAll(io.LimitReader(reader, 2<<20+1))
		reader.Close()
		if e != nil {
			return e
		}
		if name == flasher.BootTZFile {
			if digest(b) != bootTZSourceSHA256 {
				return errors.New("TZ source changed")
			}
			b, e = flasher.CompactBootFirmwareELF(b)
			if e != nil {
				return e
			}
		}
		want, capacity := flasher.BootTZSHA256, f.TZ.Range.Length
		if name == flasher.BootHYPFile {
			want, capacity = flasher.BootHYPSHA256, f.HYP.Range.Length
		}
		if digest(b) != want || uint64(len(b)) > capacity {
			return errors.New("boot firmware payload does not fit verified recipe")
		}
		if e = os.WriteFile(filepath.Join(bundle, name), b, 0600); e != nil {
			return e
		}
		found++
	}
	if found != 2 {
		return errors.New("boot firmware code missing")
	}
	return nil
}

// Upgrade only the missing boot contract of a saved operation. Existing prepared
// system/data/password hashes and the installation identity are retained; the
// old bundle is immutable evidence. No plaintext credentials are needed on retry.
func upgradePreparedBootFirmware(ctx context.Context, p Paths, d flasher.Reader, r flasher.HardwareReport, profile flasher.HardwareProfile, record, images string) (string, *flasher.BootFirmware, error) {
	fail := func(e error) (string, *flasher.BootFirmware, error) { return "", nil, e }
	m, e := verifyManifest(images)
	if e != nil {
		return fail(e)
	}
	var att flasher.ImageAttestation
	if e = readJSON(filepath.Join(images, "HardwareInputs.json"), &att); e != nil {
		return fail(e)
	}
	if att.Version != flasher.Schema || !flasher.EqualDevice(att.Device, r.Device) || att.Profile != profile.ID || !flasher.CompatibleBundle(profile, att.Bundle) {
		return fail(errors.New("saved image attestation differs from target"))
	}
	var receipt wizardReceipt
	if e = readJSON(filepath.Join(images, "Preparation.json"), &receipt); e != nil {
		return fail(e)
	}
	h, e := hashFile(filepath.Join(images, "Preparation.json"))
	if e != nil {
		return fail(e)
	}
	rh, e := hashFile(record)
	if e != nil {
		return fail(e)
	}
	if h != att.ReceiptSHA256 || receipt.RecordSHA256 != rh {
		return fail(errors.New("saved preparation/installation record changed"))
	}
	if att.Images["boot.img"] != receipt.Boot || att.Images["rootfs.ext4"] != receipt.Rootfs || receipt.Data != "" && att.Images["data.ext4"] != receipt.Data {
		return fail(errors.New("saved receipt/payload mismatch"))
	}
	for name, want := range att.Images {
		h, e := hashFile(filepath.Join(images, name))
		if e != nil {
			return fail(e)
		}
		if h != want {
			return fail(errors.New("saved payload changed"))
		}
	}
	f, e := flasher.InspectBootFirmware(ctx, d, r, profile.BootFirmwarePolicy)
	if e != nil {
		return fail(e)
	}
	if att.BootFirmware != nil {
		if flasher.Hash(att.BootFirmware) != flasher.Hash(f) {
			return fail(errors.New("saved firmware contract changed"))
		}
		return images, f, nil
	}
	stage, e := os.MkdirTemp(filepath.Dir(images), ".boot-firmware-migration-")
	if e != nil {
		return fail(e)
	}
	defer os.RemoveAll(stage)
	if e = copyTree(images, stage); e != nil {
		return fail(e)
	}
	if e = stageBootFirmware(p, stage, f); e != nil {
		return fail(e)
	}
	receipt.BootFirmware = f
	receipt.Fingerprint = flasher.Hash([]any{receipt.Fingerprint, f})
	if e = writeJSONAtomic(filepath.Join(stage, "Preparation.json"), receipt); e != nil {
		return fail(e)
	}
	att.BootFirmware = f
	att.ReceiptSHA256, e = hashFile(filepath.Join(stage, "Preparation.json"))
	if e != nil {
		return fail(e)
	}
	for _, pair := range f.Files() {
		att.Images[pair[1]], e = hashFile(filepath.Join(stage, pair[1]))
		if e != nil {
			return fail(e)
		}
	}
	if e = finalize(stage, m); e != nil {
		return fail(e)
	}
	if e = FinalizeWizardAttestation(stage, att); e != nil {
		return fail(e)
	}
	if e = ctx.Err(); e != nil {
		return fail(e)
	}
	dest := images + ".boot-firmware-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if e = os.Rename(stage, dest); e != nil {
		return fail(e)
	}
	return dest, f, nil
}
