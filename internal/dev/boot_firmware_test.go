package dev

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"testing"
)

type migrationFirmwareReader struct {
	info  flasher.DeviceInfo
	parts []flasher.Partition
}

func (d migrationFirmwareReader) Info(context.Context) (flasher.DeviceInfo, error) {
	return d.info, nil
}
func (d migrationFirmwareReader) SecureBoot(context.Context) (string, error) { return "disabled", nil }
func (d migrationFirmwareReader) Read(_ context.Context, r flasher.Range, w io.Writer) error {
	b := make([]byte, r.Length)
	copy(b, "\x7fELF")
	b[4] = 1
	b[5] = 1
	b[6] = 1
	le := binary.LittleEndian
	le.PutUint16(b[16:], 2)
	le.PutUint16(b[18:], 40)
	le.PutUint32(b[20:], 1)
	le.PutUint32(b[28:], 64)
	le.PutUint16(b[40:], 52)
	le.PutUint16(b[42:], 32)
	le.PutUint16(b[44:], 1)
	le.PutUint32(b[64:], 1)
	le.PutUint32(b[68:], 512)
	le.PutUint32(b[72:], 0x86500000)
	le.PutUint32(b[76:], 0x86500000)
	le.PutUint32(b[24:], 0x86500000)
	if r.Offset == d.parts[1].Range.Offset {
		le.PutUint32(b[24:], 0x86400000)
		le.PutUint32(b[72:], 0x86400000)
		le.PutUint32(b[76:], 0x86400000)
	}
	le.PutUint32(b[80:], 512)
	le.PutUint32(b[84:], 512)
	le.PutUint32(b[92:], 512)
	copy(b[512:], "TZ.BF.2.2-synthetic")
	_, e := w.Write(b)
	return e
}
func TestSavedBootContractMigrationPreservesIdentityPayloadAndEvidence(t *testing.T) {
	p := testPaths(t)
	source := p.Work
	p.Work = t.TempDir()
	if e := copyFile(filepath.Join(source, "buildroot-downloads/microstick-boot-firmware-v1.zip"), filepath.Join(p.Work, "buildroot-downloads/microstick-boot-firmware-v1.zip"), 0600); e != nil {
		t.Fatal(e)
	}
	info := flasher.DeviceInfo{Identity: flasher.Identity{Kind: "sahara", Hardware: "synthetic-chip"}, Geometry: flasher.Geometry{SectorSize: 512, Areas: map[string]uint64{"user": 4 << 20}}}
	parts := []flasher.Partition{{Name: "tz", GUID: "synthetic-tz", Range: flasher.Range{Area: "user", Offset: 1 << 20, Length: 512 << 10}}, {Name: "hyp", GUID: "synthetic-hyp", Range: flasher.Range{Area: "user", Offset: 2 << 20, Length: 64 << 10}}}
	reader := migrationFirmwareReader{info, parts}
	r := flasher.HardwareReport{Device: info, GPT: flasher.PartitionTable{Partitions: parts}}
	profile := flasher.HardwareProfile{ID: "synthetic-soc-profile", BootFirmwarePolicy: flasher.BootFirmwarePolicy, Bundle: map[string]string{}}
	images := filepath.Join(p.Work, "old-images")
	if e := os.Mkdir(images, 0700); e != nil {
		t.Fatal(e)
	}
	record := filepath.Join(p.Work, "record.json")
	mustWrite(t, record, "private synthetic installation identity")
	att := flasher.ImageAttestation{Version: flasher.Schema, Device: info, Profile: profile.ID, Bundle: profile.Bundle, Images: map[string]string{}}
	for _, name := range []string{"boot.img", "rootfs.ext4", "data.ext4"} {
		mustWrite(t, filepath.Join(images, name), "previously prepared "+name)
		att.Images[name], _ = hashFile(filepath.Join(images, name))
	}
	rh, _ := hashFile(record)
	receipt := wizardReceipt{Version: 1, Fingerprint: digest([]byte("old request")), Boot: att.Images["boot.img"], Rootfs: att.Images["rootfs.ext4"], Data: att.Images["data.ext4"], RecordSHA256: rh}
	if e := writeJSONAtomic(filepath.Join(images, "Preparation.json"), receipt); e != nil {
		t.Fatal(e)
	}
	att.ReceiptSHA256, _ = hashFile(filepath.Join(images, "Preparation.json"))
	if e := writeJSONAtomic(filepath.Join(images, "HardwareInputs.json"), att); e != nil {
		t.Fatal(e)
	}
	if e := finalize(images, Manifest{}); e != nil {
		t.Fatal(e)
	}
	oldManifest, _ := hashFile(filepath.Join(images, "MANIFEST.json"))
	next, fw, e := upgradePreparedBootFirmware(context.Background(), p, reader, r, profile, record, images)
	if e != nil {
		t.Fatal(e)
	}
	if next == images || !fw.Upgrade {
		t.Fatal("old images overwritten or upgrade omitted")
	}
	for _, name := range []string{"boot.img", "rootfs.ext4", "data.ext4"} {
		a, _ := os.ReadFile(filepath.Join(images, name))
		b, _ := os.ReadFile(filepath.Join(next, name))
		if !bytes.Equal(a, b) {
			t.Fatal("prepared user payload changed", name)
		}
	}
	mh, _ := hashFile(filepath.Join(images, "MANIFEST.json"))
	if mh != oldManifest {
		t.Fatal("old evidence changed")
	}
	got, _ := hashFile(record)
	if got != rh {
		t.Fatal("installation identity changed")
	}
	if _, e = verifyManifest(next); e != nil {
		t.Fatal(e)
	}
	var updated flasher.ImageAttestation
	if e = readJSON(filepath.Join(next, "HardwareInputs.json"), &updated); e != nil {
		t.Fatal(e)
	}
	if updated.Images[flasher.BootTZFile] != flasher.BootTZSHA256 || updated.Images[flasher.BootHYPFile] != flasher.BootHYPSHA256 {
		t.Fatal("pinned firmware missing")
	}
	names, e := files(next)
	if e != nil {
		t.Fatal(e)
	}
	if len(names) != 9 {
		t.Fatalf("unexpected archive extraction: %v", names)
	}
	again, _, e := upgradePreparedBootFirmware(context.Background(), p, reader, r, profile, record, next)
	if e != nil || again != next {
		t.Fatal("migration retry changed prepared identity", e)
	}
}
