package flasher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreRejectsForeignCorruptAndSwappedWithoutWrites(t *testing.T) {
	for _, scenario := range []string{"foreign", "incomplete", "corrupt", "swapped"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			backup := f.backup(t)
			plan, e := BuildRestorePlan(testCtx, f.d, backup, f.mapper)
			if e != nil {
				t.Fatal(e)
			}
			switch scenario {
			case "foreign", "incomplete":
				m, e := LoadBackup(backup)
				if e != nil {
					t.Fatal(e)
				}
				if scenario == "foreign" {
					m.Device.Identity.Hardware = "other-unit-identical-geometry"
				} else {
					m.Complete = false
				}
				if e = SaveJSON(filepath.Join(backup, "BackupManifest.json"), m); e != nil {
					t.Fatal(e)
				}
			case "corrupt":
				writeTest(t, filepath.Join(backup, plan.Operations[0].File), []byte("corrupt"))
			case "swapped":
				f.d.info.Identity.Hardware = "changed-after-selection"
			}
			if _, e = BuildRestorePlan(testCtx, f.d, backup, f.mapper); e == nil {
				t.Fatal("invalid ownership/backup accepted")
			}
			spy := &spyWriter{FileDevice: f.d}
			if e = Execute(testCtx, spy, plan, backup, backup, filepath.Join(f.root, "reject.jsonl"), Hash(plan), nil, f.mapper); e == nil || spy.writes != 0 {
				t.Fatal("restore was not rejected before writes", e, spy.writes)
			}
		})
	}
}

func TestWarmRestoreRejectsOtherCalibrationForStorageAndLegacy(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "storage", true: "legacy"}[legacy], func(t *testing.T) {
			f := newFixture(t)
			for area, env := range map[string]string{"user": "MICROSTICK_NATIVE_TEST_IMAGE", "boot1": "MICROSTICK_NATIVE_TEST_BOOT1", "boot2": "MICROSTICK_NATIVE_TEST_BOOT2"} {
				t.Setenv(env, filepath.Join(f.root, area))
			}
			t.Setenv("MICROSTICK_NATIVE_TEST_WARM", "1")
			d, e := OpenEDL(context.Background(), compileNativePlanHarness(t), "unused", "")
			if e != nil {
				t.Fatal(e)
			}
			defer d.Close()
			r, e := Inspect(testCtx, d, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			backup := filepath.Join(f.root, "own-backup")
			m, e := CreateBackup(testCtx, d, r, backup, BackupOptions{AllowFull: true, Mapper: f.mapper})
			if e != nil {
				t.Fatal(e)
			}
			if legacy {
				m.Device.Identity = Identity{Kind: "sahara", Hardware: "00000001:0000000000000002"}
				if e = SaveJSON(filepath.Join(backup, "BackupManifest.json"), m); e != nil {
					t.Fatal(e)
				}
			}
			for _, p := range r.GPT.Partitions {
				if p.Name == "persist" {
					if e = f.d.Write(testCtx, p.Range, bytes.NewReader(bytes.Repeat([]byte{0x79}, int(p.Range.Length)))); e != nil {
						t.Fatal(e)
					}
				}
			}
			before, _ := os.ReadFile(filepath.Join(f.root, "user"))
			if e = d.BindOwnBackup(testCtx, backup, m); e == nil {
				t.Fatal("foreign calibration accepted for listing")
			}
			if _, e = BuildRestorePlan(testCtx, d, backup, f.mapper); e == nil {
				t.Fatal("foreign calibration accepted for restore")
			}
			after, _ := os.ReadFile(filepath.Join(f.root, "user"))
			_, written := d.Traffic()
			if !bytes.Equal(before, after) || written != 0 {
				t.Fatal("ownership rejection wrote device bytes")
			}
		})
	}
}
