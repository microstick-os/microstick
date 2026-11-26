package flasher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWarmNativeInstallAndDamagedGPTRestore(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "storage"
		if legacy {
			name = "legacy-sahara"
		}
		t.Run(name, func(t *testing.T) { warmNativeRestore(t, legacy) })
	}
}
func warmNativeRestore(t *testing.T, legacy bool) {
	f := newFixture(t)
	f.report = addTestFirmware(t, f, 64, 64, 0)
	before, e := HashFile(filepath.Join(f.root, "user"))
	if e != nil {
		t.Fatal(e)
	}
	for area, env := range map[string]string{"user": "MICROSTICK_NATIVE_TEST_IMAGE", "boot1": "MICROSTICK_NATIVE_TEST_BOOT1", "boot2": "MICROSTICK_NATIVE_TEST_BOOT2"} {
		t.Setenv(env, filepath.Join(f.root, area))
	}
	t.Setenv("MICROSTICK_NATIVE_TEST_WARM", "1")
	helper := compileNativePlanHarness(t)
	d, e := OpenEDL(context.Background(), helper, "unused", "")
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	initial, e := d.Info(testCtx)
	if e != nil || initial.Identity.Kind != "firehose" || initial.Identity.Hardware != "" {
		t.Fatal("invented Sahara identity", e)
	}
	r, e := Inspect(testCtx, d, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.Device.Identity.Kind != "storage" {
		t.Fatal("warm session not bound to actual storage")
	}
	profile := SimulatorProfile(r)
	profile.ID = "native-warm-fixture"
	profile.Recipe = "preserve-gpt-v1"
	profile.BootFirmwarePolicy = BootFirmwarePolicy
	r.Resolution = Resolve(r, []HardwareProfile{profile})
	if r.Resolution.Status != "matched" {
		t.Fatal(r.Resolution)
	}
	backup := filepath.Join(f.root, "native-warm-backup")
	if _, e = CreateBackup(testCtx, d, r, backup, BackupOptions{AllowFull: true, FullPartitions: true, Mapper: f.mapper}); e != nil {
		t.Fatal(e)
	}
	att := ImageAttestation{Version: Schema, Device: r.Device, Profile: profile.ID, Bundle: profile.Bundle, Images: map[string]string{}}
	att.BootFirmware, e = InspectBootFirmware(testCtx, d, r, BootFirmwarePolicy)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(f.root, "fs.ext4"))
	if e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(f.images, "rootfs.ext4"), raw)
	runTest(t, "debugfs", "-w", "-R", "rm /state", filepath.Join(f.images, "rootfs.ext4"))
	if e = CreatePayload(testCtx, f.images, f.mapper); e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(f.images, "Preparation.json"), []byte("test receipt"))
	att.ReceiptSHA256, _ = HashFile(filepath.Join(f.images, "Preparation.json"))
	for _, name := range []string{"boot.img", "rootfs.ext4", "Payload.json"} {
		att.Images[name], e = HashFile(filepath.Join(f.images, name))
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = SaveJSON(filepath.Join(f.images, "HardwareInputs.json"), att); e != nil {
		t.Fatal(e)
	}
	// Test a disposable native transport with a package-private seed. Exported
	// builders only accept the compiled reviewed catalog, never this fixture.
	seed := InstallPlan{Version: Schema, Device: r.Device, Profile: profile.ID, Recipe: profile.Recipe, Mode: "install", BootFirmware: att.BootFirmware, InstallationSHA256: att.ReceiptSHA256, catalogProfile: &profile}
	plan, e := buildInstallPlan(testCtx, d, r, f.images, "", backup, seed, f.mapper)
	if e != nil {
		t.Fatal(e)
	}
	w, e := NewEDLWriter(d, plan, Hash(plan))
	if e != nil {
		t.Fatal(e)
	}
	var remoteRead uint64
	var events []Progress
	if e = Execute(testCtx, w, plan, f.images, backup, filepath.Join(f.root, "warm-write.jsonl"), Hash(plan), func(p Progress) {
		events = append(events, p)
		if p.Stage == "readback" {
			remoteRead += p.Done
		}
	}, f.mapper); e != nil {
		t.Fatal(e)
	}
	if remoteRead == 0 {
		t.Fatal("no native readback progress")
	}
	assertPlanProgress(t, plan, events, 0)
	d.Close()
	if legacy {
		m, e := LoadBackup(backup)
		if e != nil {
			t.Fatal(e)
		}
		m.Device.Identity = Identity{"sahara", "00000001:0000000000000002"}
		if e = SaveJSON(filepath.Join(backup, "BackupManifest.json"), m); e != nil {
			t.Fatal(e)
		}
	}
	file, e := os.OpenFile(filepath.Join(f.root, "user"), os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = file.WriteAt(make([]byte, 4096), 0); e != nil {
		t.Fatal(e)
	}
	file.Close()
	d, e = OpenEDL(context.Background(), helper, "unused", "")
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	restore, e := BuildRestorePlan(testCtx, d, backup, f.mapper)
	if e != nil {
		t.Fatal(e)
	}
	w, e = NewEDLWriter(d, restore, Hash(restore))
	if e != nil {
		t.Fatal(e)
	}
	events = nil
	if e = Execute(testCtx, w, restore, backup, backup, filepath.Join(f.root, "warm-restore.jsonl"), Hash(restore), func(p Progress) { events = append(events, p) }, f.mapper); e != nil {
		t.Fatal(e)
	}
	assertPlanProgress(t, restore, events, 0)
	after, e := HashFile(filepath.Join(f.root, "user"))
	if e != nil || after != before {
		t.Fatal("restore changed original bytes", e)
	}
}
