package flasher

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// These are synthetic ELF containers, not proprietary firmware payloads.
func testFirmware(class int, tz bool) []byte {
	b := make([]byte, 4096)
	copy(b, "\x7fELF")
	b[4] = byte(class / 32)
	b[5] = 1
	b[6] = 1
	le.PutUint16(b[16:], 2)
	le.PutUint16(b[18:], 40)
	le.PutUint32(b[20:], 1)
	if class == 32 {
		le.PutUint32(b[28:], 64)
		le.PutUint16(b[40:], 52)
		le.PutUint16(b[42:], 32)
		le.PutUint16(b[44:], 1)
		le.PutUint32(b[64:], 1)
		le.PutUint32(b[68:], 512)
		le.PutUint32(b[72:], 0x86500000)
		le.PutUint32(b[80:], 512)
		le.PutUint32(b[84:], 1024)
		le.PutUint32(b[92:], 512)
	} else {
		le.PutUint64(b[32:], 64)
		le.PutUint16(b[52:], 64)
		le.PutUint16(b[54:], 56)
		le.PutUint16(b[56:], 1)
		le.PutUint32(b[64:], 1)
		le.PutUint64(b[72:], 512)
		le.PutUint64(b[80:], 0x86500000)
		le.PutUint64(b[96:], 512)
		le.PutUint64(b[104:], 1024)
		le.PutUint64(b[112:], 512)
	}
	if tz {
		version := "TZ.BF.3.0-test"
		if class == 32 {
			version = "TZ.BF.2.2-test"
		}
		copy(b[512:], version)
	}
	base := uint32(0x86500000)
	if !tz {
		base = 0x86400000
	}
	if class == 32 {
		le.PutUint32(b[24:], base)
		le.PutUint32(b[72:], base)
		le.PutUint32(b[76:], base)
	} else {
		le.PutUint64(b[24:], uint64(base))
		le.PutUint64(b[80:], uint64(base))
		le.PutUint64(b[88:], uint64(base))
		if !tz {
			le.PutUint16(b[18:], 183)
		}
	}
	return b
}
func addTestFirmware(t *testing.T, f fixture, tz, hyp int, shift uint64) HardwareReport {
	t.Helper()
	file, e := os.OpenFile(filepath.Join(f.root, "user"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	parts := append([]Partition(nil), f.report.GPT.Partitions...)
	if shift != 0 {
		for i := range parts {
			parts[i].GUID += "-different-device"
		}
	}
	parts = append(parts, Partition{Name: "tz", Range: Range{"user", 20<<20 + shift, 512 << 10}}, Partition{Name: "hyp", Range: Range{"user", 21<<20 + shift, 128 << 10}})
	makeGPT(t, file, f.report.Device.Geometry.Areas["user"]/512, parts)
	for i, p := range parts[len(parts)-2:] {
		class := tz
		if i == 1 {
			class = hyp
		}
		if _, e = file.WriteAt(testFirmware(class, i == 0), int64(p.Range.Offset)); e != nil {
			t.Fatal(e)
		}
	}
	r := f.report
	r.GPT, e = ReadGPT(testCtx, f.d, r.Device)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

type securityDevice struct {
	*FileDevice
	security       string
	probes, writes int
	failAt         int
	cancel         context.CancelFunc
}

func (d *securityDevice) SecureBoot(context.Context) (string, error) {
	d.probes++
	if d.security == "error" {
		return "", errors.New("probe failed")
	}
	return d.security, nil
}
func (d *securityDevice) Write(ctx context.Context, r Range, v io.Reader) error {
	d.writes++
	if d.writes == d.failAt {
		b := make([]byte, 512)
		if _, e := io.ReadFull(v, b); e != nil {
			return e
		}
		if e := d.FileDevice.Write(ctx, Range{r.Area, r.Offset, 512}, bytes.NewReader(b)); e != nil {
			return e
		}
		return errors.New("injected transfer failure")
	}
	e := d.FileDevice.Write(ctx, r, v)
	if d.cancel != nil && d.writes == 2 {
		d.cancel()
	}
	return e
}

func TestBootFirmwareInspectionIsBoundedAndHardwareBased(t *testing.T) {
	for _, kind := range []string{"legacy", "modern", "mixed", "enabled", "unknown", "unavailable", "missing", "corrupt", "unknown-abi", "unknown-map", "small", "changed-guid", "shifted"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			tc, hc := 32, 32
			if kind == "modern" {
				tc, hc = 64, 64
			}
			if kind == "mixed" {
				tc = 64
			}
			var shift uint64
			if kind == "shifted" {
				shift = 64 << 10
			}
			r := addTestFirmware(t, f, tc, hc, shift)
			d := &securityDevice{FileDevice: f.d, security: "disabled"}
			switch kind {
			case "enabled", "unknown":
				d.security = kind
			case "unavailable":
				d.security = "error"
			case "missing":
				r.GPT.Partitions = r.GPT.Partitions[:len(r.GPT.Partitions)-1]
			case "small":
				r.GPT.Partitions[len(r.GPT.Partitions)-2].Range.Length = 4096
			case "changed-guid":
				r.GPT.Partitions[len(r.GPT.Partitions)-2].GUID = "different-unit-guid"
			case "corrupt", "unknown-abi", "unknown-map":
				b := testFirmware(32, true)
				if kind == "corrupt" {
					b[5] = 2
				} else if kind == "unknown-map" {
					le.PutUint32(b[76:], 0x90000000)
				} else {
					copy(b[512:], "TZ.UNKNOWN.test")
				}
				if e := f.d.Write(testCtx, Range{"user", r.GPT.Partitions[len(r.GPT.Partitions)-2].Range.Offset, 4096}, bytes.NewReader(b)); e != nil {
					t.Fatal(e)
				}
			}
			got, e := InspectBootFirmware(testCtx, d, r, BootFirmwarePolicy)
			good := kind == "legacy" || kind == "modern" || kind == "mixed" || kind == "changed-guid" || kind == "shifted"
			if (e == nil) != good {
				t.Fatalf("got=%+v err=%v", got, e)
			}
			if d.writes != 0 {
				t.Fatal("preflight wrote storage")
			}
			if good && got.Upgrade != (kind != "modern") {
				t.Fatal("wrong upgrade decision")
			}
			if kind == "modern" && d.probes != 0 {
				t.Fatal("existing compatible firmware needs no unsigned upgrade")
			}
		})
	}
}

func nativeTestPlan(t *testing.T, d *EDLReader, image, dir string, payload []byte) InstallPlan {
	t.Helper()
	info, e := d.Info(testCtx)
	if e != nil {
		t.Fatal(e)
	}
	file, e := os.OpenFile(image, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	parts := []Partition{{Name: "rootfs", Range: Range{"user", 64 << 10, uint64(len(payload))}}, {Name: "boot", Range: Range{"user", 128 << 10, uint64(len(payload))}}, {Name: "tz", Range: Range{"user", 256 << 10, 4096}}, {Name: "hyp", Range: Range{"user", 260 << 10, 4096}}}
	makeGPT(t, file, info.Geometry.Areas["user"]/512, parts)
	for i, part := range parts[2:] {
		if _, e = file.WriteAt(testFirmware(64, i == 0), int64(part.Range.Offset)); e != nil {
			t.Fatal(e)
		}
	}
	file.Close()
	gpt, e := ReadGPT(testCtx, d, info)
	if e != nil {
		t.Fatal(e)
	}
	r := HardwareReport{Device: info, GPT: gpt}
	fw, e := InspectBootFirmware(testCtx, d, r, BootFirmwarePolicy)
	if e != nil {
		t.Fatal(e)
	}
	profile := HardwareProfile{ID: "native-harness", Qualified: true, Recipe: "preserve-gpt-v1", BootFirmwarePolicy: BootFirmwarePolicy, Bundle: map[string]string{}}
	p := InstallPlan{Version: Schema, Device: info, Profile: profile.ID, Recipe: profile.Recipe, Mode: "quick", BackupWaived: true, BootFirmware: fw, qualification: &profile}
	for i, name := range []string{"rootfs.ext4", "boot.img"} {
		part := gpt.Partitions[i]
		before, e := HashRange(testCtx, d, part.Range)
		if e != nil {
			t.Fatal(e)
		}
		p.Targets = append(p.Targets, part)
		p.Operations = append(p.Operations, Operation{Range: part.Range, File: name, SHA256: HashBytes(payload), BeforeSHA256: before})
		p.Guards = append(p.Guards, Extent{Range: part.Range, SHA256: before})
		writeTest(t, filepath.Join(dir, name), payload)
	}
	for _, rng := range gpt.Metadata {
		h, e := HashRange(testCtx, d, rng)
		if e != nil {
			t.Fatal(e)
		}
		p.Guards = append(p.Guards, Extent{Range: rng, SHA256: h})
	}
	p.Protected = protection(p.Guards, p.Operations)
	writeTest(t, filepath.Join(dir, "Preparation.json"), []byte("synthetic receipt\n"))
	p.InstallationSHA256, _ = HashFile(filepath.Join(dir, "Preparation.json"))
	att := ImageAttestation{Version: Schema, Device: info, Profile: p.Profile, Bundle: profile.Bundle, Images: map[string]string{"boot.img": HashBytes(payload), "rootfs.ext4": HashBytes(payload)}, ReceiptSHA256: p.InstallationSHA256, BootFirmware: fw}
	if e := SaveJSON(filepath.Join(dir, "HardwareInputs.json"), att); e != nil {
		t.Fatal(e)
	}
	return p
}

func pinnedTestFirmware(t *testing.T, images string) {
	t.Helper()
	archive := filepath.Join(os.Getenv("MICROSTICK_WORKSPACE"), "buildroot-downloads/microstick-boot-firmware-v1.zip")
	h, e := HashFile(archive)
	if e != nil || h != "b609b91c94b07d7cde4141a578df5c1440fbe75140f0c645c7124356774e2418" {
		t.Fatal("pinned external firmware archive required", e)
	}
	z, e := zip.OpenReader(archive)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	for _, f := range z.File {
		var name, want string
		switch f.Name {
		case "base/tz.mbn":
			name, want = BootTZFile, BootTZSHA256
		case "base/hyp.mbn":
			name, want = BootHYPFile, BootHYPSHA256
		default:
			continue
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(io.LimitReader(r, 2<<20))
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		if name == BootTZFile {
			original := append([]byte(nil), b...)
			b, e = CompactBootFirmwareELF(b)
			if e != nil {
				t.Fatal(e)
			}
			if e = verifyFirmwareMemory(original, b); e != nil {
				t.Fatal(e)
			}
		}
		if HashBytes(b) != want {
			t.Fatal("firmware hash changed")
		}
		writeTest(t, filepath.Join(images, name), b)
	}
}

func TestFirmwareCommitRetryAndOwnRestore(t *testing.T) {
	for _, kind := range []string{"complete", "cancel-before", "cancel-in-pair", "fail-tz", "fail-hyp", "fail-boot", "secure-changed", "tamper", "shifted", "larger-storage", "gpt-changed"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			shift := uint64(0)
			if kind == "shifted" {
				shift = 64 << 10
			}
			if kind == "larger-storage" {
				file, e := os.OpenFile(filepath.Join(f.root, "user"), os.O_RDWR, 0)
				if e != nil {
					t.Fatal(e)
				}
				if e = file.Truncate(32 << 20); e != nil {
					t.Fatal(e)
				}
				file.Close()
				f.d.info.Geometry.Areas["user"] = 32 << 20
				f.report.Device, e = f.d.Info(testCtx)
				if e != nil {
					t.Fatal(e)
				}
				shift = 6 << 20
			}
			r := addTestFirmware(t, f, 32, 32, shift)
			d := &securityDevice{FileDevice: f.d, security: "disabled"}
			backup := filepath.Join(f.root, "own-stock")
			if _, e := CreateBackup(testCtx, d, r, backup, BackupOptions{AllowFull: true, FullPartitions: true, Mapper: f.mapper}); e != nil {
				t.Fatal(e)
			}
			fw, e := InspectBootFirmware(testCtx, d, r, BootFirmwarePolicy)
			if e != nil {
				t.Fatal(e)
			}
			pinnedTestFirmware(t, f.images)
			profile := SimulatorProfile(r)
			profile.BootFirmwarePolicy = BootFirmwarePolicy
			r.Resolution = Resolve(r, []HardwareProfile{profile})
			seed := InstallPlan{Version: Schema, Device: r.Device, Profile: profile.ID, Recipe: "simulator-v1", Mode: "install", BootFirmware: fw, qualification: &profile}
			plan, e := buildInstallPlan(testCtx, d, r, f.images, "", backup, seed, f.mapper)
			if e != nil {
				t.Fatal(e)
			}
			if len(plan.Operations) != 4 || plan.Operations[1].File != BootTZFile || plan.Operations[2].File != BootHYPFile || plan.Operations[3].File != "boot.img" {
				t.Fatal("wrong firmware commit order")
			}
			if kind == "secure-changed" {
				d.security = "enabled"
			}
			if kind == "tamper" {
				b, _ := os.ReadFile(filepath.Join(f.images, BootTZFile))
				b[4000] ^= 1
				writeTest(t, filepath.Join(f.images, BootTZFile), b)
			}
			if kind == "gpt-changed" {
				plan.BootFirmware.TZ.GUID = "different-guid"
			}
			switch kind {
			case "fail-tz":
				d.failAt = 2
			case "fail-hyp":
				d.failAt = 3
			case "fail-boot":
				d.failAt = 4
			}
			ctx, cancel := context.WithCancel(testCtx)
			defer cancel()
			if kind == "cancel-in-pair" {
				d.cancel = cancel
			}
			journal := filepath.Join(f.root, "commit.jsonl")
			e = Execute(ctx, d, plan, f.images, backup, journal, Hash(plan), func(v Progress) {
				if kind == "cancel-before" && v.Stage == "write" && v.Done == 0 {
					cancel()
				}
			}, f.mapper)
			success := kind == "complete" || kind == "cancel-in-pair" || kind == "shifted" || kind == "larger-storage"
			if (e == nil) != success {
				t.Fatalf("writes=%d err=%v", d.writes, e)
			}
			if kind == "secure-changed" || kind == "tamper" || kind == "gpt-changed" {
				if d.writes != 0 {
					t.Fatal("unsafe preflight wrote storage")
				}
				return
			}
			if kind == "cancel-before" {
				if d.writes != 1 {
					t.Fatal("cancellation crossed commit boundary")
				}
				return
			}
			if !success {
				b, _ := os.ReadFile(journal)
				if !bytes.Contains(b, []byte("recovery-required")) {
					t.Fatal("missing recovery-required journal")
				}
				d.failAt = 0
				resume, e := ResumePlan(testCtx, d, plan, profile)
				if e != nil {
					t.Fatal("resume", e)
				}
				if e = Execute(testCtx, d, resume, f.images, backup, journal, Hash(resume), nil, f.mapper); e != nil {
					t.Fatal("retry", e)
				}
			}
			for _, op := range plan.Operations {
				h, e := HashRange(testCtx, d, op.Range)
				if e != nil || h != op.SHA256 {
					t.Fatal("payload not committed", op.File, e)
				}
			}
			restore, e := BuildRestorePlan(testCtx, d, backup, f.mapper)
			if e != nil {
				t.Fatal(e)
			}
			if e = Execute(testCtx, d, restore, backup, backup, filepath.Join(f.root, "restore.jsonl"), Hash(restore), nil, f.mapper); e != nil {
				t.Fatal(e)
			}
			original, e := InspectBootFirmware(testCtx, d, r, BootFirmwarePolicy)
			if e != nil || Hash(original) != Hash(fw) {
				t.Fatal("own factory firmware not restored", e)
			}
		})
	}
}

func TestNativeLegacyPlanCannotBypassBootContract(t *testing.T) {
	f := newFixture(t)
	d := &EDLReader{}
	p := InstallPlan{Version: Schema, Device: f.report.Device, Profile: "legacy", Recipe: "preserve-gpt-v1", Mode: "quick", BackupWaived: true, Operations: []Operation{{Range: Range{"user", 32768, 512}, File: "boot.img", SHA256: HashBytes(nil), BeforeSHA256: HashBytes(nil)}}, Guards: []Extent{{Range: Range{"user", 0, 512}, SHA256: HashBytes(nil)}}}
	p.Device.Identity = Identity{"sahara", "test-only"}
	if ValidatePlan(p) == nil {
		t.Fatal("legacy native plan accepted")
	}
	if _, e := NewEDLWriter(d, p, Hash(p)); e == nil {
		t.Fatal("legacy native writer accepted")
	}
}

func TestNativeQualificationCannotComeFromEditedJSON(t *testing.T) {
	f := newFixture(t)
	p := SimulatorProfile(f.report)
	p.Recipe = "preserve-gpt-v1"
	p.BootFirmwarePolicy = BootFirmwarePolicy
	if _, e := BuildQualifiedInstallPlan(testCtx, f.d, f.report, p, f.images, "", ""); !errors.Is(e, ErrUnsupported) {
		t.Fatal("unreviewed native profile accepted", e)
	}
	catalog, e := Catalog()
	if e != nil {
		t.Fatal(e)
	}
	for _, profile := range catalog {
		if !profile.Qualified {
			continue
		}
		if !canonicalNativeProfile(profile) {
			t.Fatal("catalog profile rejected")
		}
		profile.BootFirmwarePolicy = ""
		if canonicalNativeProfile(profile) {
			t.Fatal("edited native profile accepted")
		}
	}
}
