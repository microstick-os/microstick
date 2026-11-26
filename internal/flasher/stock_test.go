package flasher

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewedBoardSetsDoNotDeclareMapsEquivalent(t *testing.T) {
	f := newFixture(t)
	r := f.report
	p := fixtureProfile(r)
	p.Board = "hmuf02"
	p.Layout = "boot-rootfs"
	// A reviewed alternative may retain findings, but a changed finding/map/selector
	// must invalidate the exact review. No generic suppression is permitted.
	r.Variants[0].Issues = []string{"reviewed downstream telephone alternative"}
	other := r.Variants[0]
	other.SHA256 = HashBytes([]byte("different tree"))
	other.MapSHA256 = HashBytes([]byte("different map"))
	r.Variants = append(r.Variants, other)
	p.SourceSets = []ReviewedSourceSet{{ID: "reviewed", Fingerprint: SourceSetFingerprint(r.Variants), Review: "review"}}
	for _, kind := range []string{"confirmed", "unconfirmed", "map", "issue", "selector", "extra", "missing", "wrong-layout"} {
		t.Run(kind, func(t *testing.T) {
			rr := r
			rr.Variants = append([]DTVariant(nil), r.Variants...)
			switch kind {
			case "unconfirmed":
			case "map":
				rr.Variants[0].MapSHA256 = HashBytes([]byte("unknown"))
			case "issue":
				rr.Variants[0].Issues = []string{"new real conflict"}
			case "selector":
				rr.Variants[0].Selectors = [][]uint32{{99}}
			case "extra":
				rr.Variants = append(rr.Variants, other)
			case "missing":
				rr.Variants = rr.Variants[:1]
			case "wrong-layout":
				rr.GPT = PartitionTable{}
			}
			got := Resolve(rr, []HardwareProfile{p})
			if (got.Status == "matched") != (kind == "confirmed" || kind == "unconfirmed") {
				t.Fatalf("%s: %+v", kind, got)
			}
		})
	}
}

func TestBoardCompatibilityIgnoresIdentityAndBuildMetadata(t *testing.T) {
	f := newFixture(t)
	r := f.report
	p := fixtureProfile(r)
	p.Board = "hmuf02"
	p.Layout = "boot-rootfs"
	p.SourceSets = []ReviewedSourceSet{{ID: "reviewed", Fingerprint: SourceSetFingerprint(r.Variants), CompatibilityFingerprint: CompatibilitySetFingerprint(r.Variants), Review: "reviewed hardware map"}}
	for _, kind := range []string{"metadata", "gpio", "unknown-property"} {
		t.Run(kind, func(t *testing.T) {
			data := makeDT(t, "")
			switch kind {
			case "metadata":
				data = bytes.ReplaceAll(data, []byte("PRIVATE-SERIAL-SENTINEL"), []byte("ANOTHER-SERIAL-SENTINEL"))
				data = bytes.ReplaceAll(data, []byte("PRIVATE-BOOTARGS-SENTINEL"), []byte("ANOTHER-BOOTARGS-SENTINEL"))
			case "gpio":
				data = makeDT(t, "danger { gpios=<&gpio 72 0>; };")
			case "unknown-property":
				data = makeDT(t, "unreviewed { qcom,unknown-power-config=<1>; };")
			}
			vs, e := DTBs(data, "boot")
			if e != nil {
				t.Fatal(e)
			}
			rr := r
			rr.Variants = vs
			got := Resolve(rr, []HardwareProfile{p})
			if (got.Status == "matched") != (kind == "metadata") {
				t.Fatalf("%s: %+v", kind, got)
			}
			if kind == "metadata" && vs[0].SHA256 == r.Variants[0].SHA256 {
				t.Fatal("fixture did not change DTB bytes")
			}
		})
	}
}
func TestCompositeBootProvenanceAndBounds(t *testing.T) {
	lk, e := os.ReadFile(filepath.Join(os.Getenv("MICROSTICK_WORKSPACE"), "buildroot-downloads/lk2nd-msm8916-23.1.img"))
	if e != nil {
		t.Fatal(e)
	}
	dt := makeDT(t, "")
	inner := make([]byte, 4096)
	copy(inner, "ANDROID!")
	binary.LittleEndian.PutUint32(inner[8:], uint32(len(dt)))
	binary.LittleEndian.PutUint32(inner[36:], 2048)
	copy(inner[2048:], dt)
	b, e := CompositeBoot(lk, inner, 16<<20)
	if e != nil {
		t.Fatal(e)
	}
	p, vs, e := BootDTBs(b)
	if e != nil || len(vs) != 1 || vs[0].Offset != LK2ndOffset+2048 || p.DTBOrigin != "boot:inner" || vs[0].SHA256 != HashBytes(dt) {
		t.Fatal(p, vs, e)
	}
	for _, kind := range []string{"outer", "padding", "inner", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			bad := append([]byte(nil), b...)
			switch kind {
			case "outer":
				bad[100] ^= 1
			case "padding":
				bad[LK2ndBytes] = 1
			case "inner":
				bad[LK2ndOffset] = 0
			case "truncated":
				bad = bad[:LK2ndOffset]
			}
			if _, _, e := BootDTBs(bad); e == nil {
				t.Fatal("bad composite accepted")
			}
		})
	}
	if _, e := CompositeBoot(lk, inner, uint64(len(b)-1)); e == nil {
		t.Fatal("oversize boot accepted")
	}
	if _, e := CompositeBoot(bytes.Repeat([]byte{1}, len(lk)), inner, 16<<20); e == nil {
		t.Fatal("unpinned lk accepted")
	}
}
func TestStockStorageAndPlanGates(t *testing.T) {
	f := newFixture(t)
	r := f.report
	for i := range r.GPT.Partitions {
		if r.GPT.Partitions[i].Name == "rootfs" {
			r.GPT.Partitions[i].Name = "system"
		}
	}
	r.GPT.Partitions = append(r.GPT.Partitions, Partition{Name: "userdata", GUID: "data-guid", Range: Range{"user", 18 << 20, 4 << 20}})
	if _, e := StockStorage(r, false, false); e == nil {
		t.Fatal("format without confirmation")
	}
	s, e := StockStorage(r, false, true)
	if e != nil {
		t.Fatal(e)
	}
	r.Boot.Mode = "lk2nd-23.1"
	preserve, e := StockStorage(r, false, false)
	if e != nil || preserve.DataMode != "preserve" {
		t.Fatal(preserve, e)
	}
	p := InstallPlan{Version: Schema, Device: r.Device, Profile: "microstick-hmuf02-system-linux17", Recipe: StockRecipe, Mode: "install", Storage: &s, InstallationSHA256: HashBytes(nil), BackupSHA256: HashBytes(nil), Guards: []Extent{{Range: s.Boot.Range, SHA256: HashBytes(nil)}}}
	for i, part := range []Partition{s.Data, s.Root, s.Boot} {
		p.Operations = append(p.Operations, Operation{Range: part.Range, File: []string{"data.ext4", "rootfs.ext4", "boot.img"}[i], SHA256: HashBytes(nil), BeforeSHA256: HashBytes(nil)})
	}
	for _, rng := range []Range{{"user", 0, 512}, {"boot1", 0, 4096}} {
		x := Extent{Range: rng, SHA256: HashBytes(nil)}
		p.Guards = append(p.Guards, x)
		p.Protected = append(p.Protected, x)
	}
	if e := ValidatePlan(p); e != nil {
		t.Fatal(e)
	}
	t.Run("primary-firmware-contract", func(t *testing.T) {
		f := &BootFirmware{Version: 1, Policy: BootFirmwarePolicy, TZ: Partition{Name: "tz", GUID: "varied-tz-guid", Range: Range{"user", 22 << 20, 512 << 10}}, HYP: Partition{Name: "hyp", GUID: "varied-hyp-guid", Range: Range{"user", 23 << 20, 64 << 10}}, TZClass: 32, HYPClass: 32, TZSHA256: HashBytes(nil), HYPSHA256: HashBytes(nil), Upgrade: true, Security: "disabled"}
		q := p
		q.BootFirmware = f
		q.Operations = append([]Operation(nil), p.Operations[:2]...)
		q.Operations = append(q.Operations, Operation{Range: Range{"user", f.TZ.Range.Offset, BootTZBytes}, File: BootTZFile, SHA256: BootTZSHA256, BeforeSHA256: HashBytes(nil)}, Operation{Range: Range{"user", f.HYP.Range.Offset, BootHYPBytes}, File: BootHYPFile, SHA256: BootHYPSHA256, BeforeSHA256: HashBytes(nil)}, p.Operations[2])
		if e := ValidatePlan(q); e != nil {
			t.Fatal("five-operation plan", e)
		}
		for _, kind := range []string{"missing-pair", "wrong-code", "backup-slot", "wrong-offset"} {
			t.Run(kind, func(t *testing.T) {
				x := q
				x.Operations = append([]Operation(nil), q.Operations...)
				switch kind {
				case "missing-pair":
					x.Operations = append(x.Operations[:3], x.Operations[4:]...)
				case "wrong-code":
					x.Operations[2].SHA256 = HashBytes(nil)
				case "backup-slot":
					x.Operations[2].File = "tzbak.mbn"
				case "wrong-offset":
					x.Operations[2].Range.Offset += 512
				}
				if ValidatePlan(x) == nil {
					t.Fatal("invalid firmware operation accepted")
				}
			})
		}
	})
	for _, kind := range []string{"quick-format", "order", "target", "short-root", "missing-protection", "receipt"} {
		t.Run(kind, func(t *testing.T) {
			q := p
			q.Operations = append([]Operation(nil), p.Operations...)
			switch kind {
			case "quick-format":
				q.Mode = "quick"
				q.BackupWaived = true
			case "order":
				q.Operations[0], q.Operations[2] = q.Operations[2], q.Operations[0]
			case "target":
				q.Operations[1].Range.Offset += 512
			case "short-root":
				q.Operations[1].Range.Length -= 512
			case "missing-protection":
				q.Protected = q.Protected[1:]
			case "receipt":
				q.InstallationSHA256 = ""
			}
			if e := ValidatePlan(q); e == nil {
				t.Fatal("unsafe stock plan accepted")
			}
		})
	}
}

func TestFullPartitionBackupSupportsExactStockRollback(t *testing.T) {
	f := newFixture(t)
	allocated, e := AllocationMap(testCtx, f.d, f.report.GPT.Partitions[2].Range, f.mapper)
	if e != nil {
		t.Fatal(e)
	}
	root := f.report.GPT.Partitions[2].Range
	// Put data in an unallocated sector: a compact backup deliberately omits it,
	// while a stock-transition full partition backup must retain it.
	var free Range
	for off := root.Offset; off < root.Offset+root.Length; off += 512 {
		used := false
		for _, x := range allocated {
			if off >= x.Offset && off < x.Offset+x.Length {
				used = true
				break
			}
		}
		if !used {
			free = Range{"user", off, 512}
			break
		}
	}
	if free.Length == 0 {
		t.Fatal("fixture has no free blocks")
	}
	payload := bytes.Repeat([]byte{0x5b}, 512)
	if e = f.d.Write(testCtx, free, bytes.NewReader(payload)); e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(f.root, "full-backup")
	m, e := CreateBackup(testCtx, f.d, f.report, backup, BackupOptions{Mapper: f.mapper, AllowFull: true, FullPartitions: true})
	if e != nil {
		t.Fatal(e)
	}
	if m.FreeBlocksOmitted {
		t.Fatal("full backup silently omitted free blocks")
	}
	for _, region := range m.Regions {
		if region.Mode != "full" || len(region.Allocated) != 1 || region.Allocated[0] != region.Range {
			t.Fatal("full partition coverage lost")
		}
	}
	if e = f.d.Write(testCtx, free, bytes.NewReader(make([]byte, 512))); e != nil {
		t.Fatal(e)
	}
	plan, e := BuildRestorePlan(testCtx, f.d, backup, f.mapper)
	if e != nil {
		t.Fatal(e)
	}
	if e = Execute(testCtx, f.d, plan, backup, backup, filepath.Join(f.root, "full-restore.jsonl"), Hash(plan), nil, f.mapper); e != nil {
		t.Fatal(e)
	}
	got, e := ReadRange(testCtx, f.d, free, 512)
	if e != nil || !bytes.Equal(got, payload) {
		t.Fatal("free partition bytes not restored", e)
	}
}
func TestStockCapabilityCannotBeEnabledByJSONFlag(t *testing.T) {
	catalog, e := Catalog()
	if e != nil {
		t.Fatal(e)
	}
	own := catalog[0]
	if !canonicalStockProfile(own) {
		t.Fatal("reviewed profile not accepted")
	}
	own.Bundle = map[string]string{"dtb": HashBytes(nil)}
	if canonicalStockProfile(own) {
		t.Fatal("edited JSON granted stock capability")
	}
}
