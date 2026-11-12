package dev

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"

	"microstick.dev/tools/internal/flasher"
)

func workflowDevice(t *testing.T, p Paths) (*flasher.FileDevice, flasher.HardwareReport, flasher.HardwareProfile, string) {
	t.Helper()
	root := filepath.Join(p.Work, "device.bin")
	f, e := os.Create(root)
	if e != nil {
		t.Fatal(e)
	}
	const total = 320 << 20
	if e = f.Truncate(total); e != nil {
		t.Fatal(e)
	}
	parts := []struct {
		name      string
		off, size uint64
	}{{"boot", 1 << 20, 16 << 20}, {"rootfs", 17 << 20, 288 << 20}, {"persist", 305 << 20, 4 << 20}}
	entries := make([]byte, 128*128)
	for i, p := range parts {
		b := entries[i*128:]
		b[0] = 1
		b[16] = byte(i + 1)
		binary.LittleEndian.PutUint64(b[32:], p.off/512)
		binary.LittleEndian.PutUint64(b[40:], (p.off+p.size)/512-1)
		for j, c := range p.name {
			binary.LittleEndian.PutUint16(b[56+j*2:], uint16(c))
		}
	}
	mbr := make([]byte, 512)
	mbr[450] = 0xee
	mbr[510] = 0x55
	mbr[511] = 0xaa
	f.WriteAt(mbr, 0)
	for _, backup := range []bool{false, true} {
		current, other, array := uint64(1), uint64(total/512-1), uint64(2)
		if backup {
			current, other, array = other, current, total/512-33
		}
		h := make([]byte, 512)
		copy(h, "EFI PART")
		binary.LittleEndian.PutUint32(h[8:], 0x10000)
		binary.LittleEndian.PutUint32(h[12:], 92)
		binary.LittleEndian.PutUint64(h[24:], current)
		binary.LittleEndian.PutUint64(h[32:], other)
		binary.LittleEndian.PutUint64(h[40:], 34)
		binary.LittleEndian.PutUint64(h[48:], total/512-34)
		h[56] = 0x41
		binary.LittleEndian.PutUint64(h[72:], array)
		binary.LittleEndian.PutUint32(h[80:], 128)
		binary.LittleEndian.PutUint32(h[84:], 128)
		binary.LittleEndian.PutUint32(h[88:], crc32.ChecksumIEEE(entries))
		binary.LittleEndian.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))
		f.WriteAt(h, int64(current*512))
		f.WriteAt(entries, int64(array*512))
	}
	boot, e := os.ReadFile(filepath.Join(p.Artifacts, "boot.img"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt(boot, 1<<20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	paths := map[string]string{"user": root}
	for _, area := range []string{"boot1", "boot2"} {
		path := filepath.Join(p.Work, area)
		mustWrite(t, path, string(make([]byte, 4096)))
		paths[area] = path
	}
	d, e := flasher.OpenFiles(paths, "workflow-unit", true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	r, e := flasher.Inspect(context.Background(), d, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	profile := flasher.SimulatorProfile(r)
	r.Resolution = flasher.Resolve(r, []flasher.HardwareProfile{profile})
	if r.Resolution.Status != "matched" {
		t.Fatal(r.Resolution)
	}
	return d, r, profile, root
}

func TestAutomaticInstallRetryUpdateAndRollback(t *testing.T) {
	p := testPaths(t)
	p.Work = t.TempDir()
	ctx := context.Background()
	mapper, _, _, e := FlasherBootstrap(p, false)
	if e != nil {
		t.Fatal(e)
	}
	d, r, profile, device := workflowDevice(t, p)
	release, e := ReadFlasherRelease(p.Artifacts)
	if e != nil {
		t.Fatal(e)
	}
	op := NewFlasherOperation(p, r, release, "install")
	options := WizardOptions{SSID: "Workflow-AP", APPassword: "workflow-ap-secret", RootPassword: "workflow-root-secret", Clean: true}
	cancelled, cancel := context.WithCancel(ctx)
	e = RunFlasherOperation(cancelled, p, d, mapper, op, profile, options, func(v flasher.Progress) {
		if v.Stage == "write" {
			cancel()
		}
	})
	if e == nil || !op.Prepared || op.Plan == nil || op.Complete {
		t.Fatal("interruption not retained", e)
	}
	pending, e := FindFlasherOperation(ctx, p, d, "install")
	if e != nil || pending == nil {
		t.Fatal("automatic retry not found", e)
	}
	if e = RunFlasherOperation(ctx, p, d, mapper, pending, profile, WizardOptions{}, nil); e != nil {
		t.Fatal("retry", e)
	}
	if !pending.Complete {
		t.Fatal("retry not completed")
	}
	r, e = flasher.Inspect(ctx, d, []flasher.HardwareProfile{profile}, nil)
	if e != nil {
		t.Fatal(e)
	}
	profile = flasher.SimulatorProfile(r)
	r.Resolution = flasher.Resolve(r, []flasher.HardwareProfile{profile})
	var system flasher.Range
	for _, part := range r.GPT.Partitions {
		if part.Name == "rootfs" {
			system = part.Range
		}
	}
	old := filepath.Join(p.Work, "old-rootfs.ext4")
	f, e := os.Create(old)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Read(ctx, system, f); e != nil {
		t.Fatal(e)
	}
	f.Close()
	mustWrite(t, filepath.Join(p.Work, "user-state"), "survives an update\n")
	if e = imagePut(old, "/etc/microstick/workflow-user-state", filepath.Join(p.Work, "user-state"), 0100600); e != nil {
		t.Fatal(e)
	}
	f, e = os.Open(old)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.Write(ctx, system, f); e != nil {
		t.Fatal(e)
	}
	f.Close()
	originalIdentity, e := imageDefault(old)
	if e != nil {
		t.Fatal(e)
	}
	retained := map[string][]byte{}
	for _, path := range []string{defaultIMEIPath, "/etc/shadow", "/etc/microstick/hostapd.conf", "/etc/microstick/workflow-user-state", "/usr/share/microstick/default-plugins/web/default-auth.json"} {
		b, e := imageBytes(old, path)
		if e != nil {
			t.Fatal(path, e)
		}
		retained[path] = b
	}
	before, e := hashFile(device)
	if e != nil {
		t.Fatal(e)
	}
	next := filepath.Join(p.Work, "release-next")
	if e = os.Mkdir(next, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"boot.img", "rootfs.ext4"} {
		if e = copyFile(filepath.Join(p.Artifacts, name), filepath.Join(next, name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	mustWrite(t, filepath.Join(p.Work, "new-system"), "fresh release\n")
	if e = imagePut(filepath.Join(next, "rootfs.ext4"), "/usr/share/microstick/workflow-new-system", filepath.Join(p.Work, "new-system"), 0100644); e != nil {
		t.Fatal(e)
	}
	if e = finalize(next, Manifest{Version: 2}); e != nil {
		t.Fatal(e)
	}
	p.Artifacts = next
	newer, e := ReadFlasherRelease(next)
	if e != nil {
		t.Fatal(e)
	}
	newer.Published = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	detected, e := ProbeInstalledRelease(ctx, d, r, mapper)
	if e != nil {
		t.Fatal(e)
	}
	if e = CheckFlasherUpdate(detected, newer); e != nil {
		t.Fatal(e)
	}
	update := NewFlasherOperation(p, r, newer, "update")
	if e = RunFlasherOperation(ctx, p, d, mapper, update, profile, WizardOptions{Update: true}, nil); e != nil {
		t.Fatal("update", e)
	}
	for path, want := range retained {
		got, e := flasher.ReadExt4File(ctx, d, system, mapper, path)
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("update changed retained state", path, e)
		}
	}
	got, e := flasher.ReadExt4File(ctx, d, system, mapper, "/usr/share/microstick/workflow-new-system")
	if e != nil || string(got) != "fresh release\n" {
		t.Fatal("release not installed", e)
	}
	got, e = flasher.ReadExt4File(ctx, d, system, mapper, defaultIMEIPath)
	if e != nil || !bytes.Contains(got, []byte(originalIdentity)) {
		t.Fatal("identity changed", e)
	}
	installed, e := ProbeInstalledRelease(ctx, d, r, mapper)
	if e != nil {
		t.Fatal(e)
	}
	if CheckFlasherUpdate(installed, newer) == nil {
		t.Fatal("same release accepted")
	}
	if _, e = flasher.ReadExt4File(ctx, d, system, mapper, "/missing-workflow-file"); e != flasher.ErrExt4FileMissing {
		t.Fatal("missing metadata", e)
	}
	restore, e := flasher.BuildRestorePlan(ctx, d, update.Backup, mapper)
	if e != nil {
		t.Fatal(e)
	}
	if e = flasher.Execute(ctx, d, restore, update.Backup, update.Backup, filepath.Join(p.Work, "restore.jsonl"), flasher.Hash(restore), nil, mapper); e != nil {
		t.Fatal(e)
	}
	manifest, e := flasher.LoadBackup(update.Backup)
	if e != nil {
		t.Fatal(e)
	}
	if !manifest.FreeBlocksOmitted {
		after, e := hashFile(device)
		if e != nil || before != after {
			t.Fatal("full rollback differs", e)
		}
	}
	for _, x := range manifest.Extents {
		h, e := flasher.HashRange(ctx, d, x.Range)
		if e != nil || h != x.SHA256 {
			t.Fatal("rollback lost saved filesystem blocks", e)
		}
	}
	for path, want := range retained {
		got, e := flasher.ReadExt4File(ctx, d, system, mapper, path)
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("rollback lost retained state", path, e)
		}
	}
	for _, saved := range []string{update.Path, pending.Path, op.Record, update.Record} {
		b, e := os.ReadFile(saved)
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{options.APPassword, options.RootPassword} {
			if bytes.Contains(b, []byte(secret)) {
				t.Fatal(fmt.Sprintf("plaintext credential in %s", filepath.Base(saved)))
			}
		}
	}
}
