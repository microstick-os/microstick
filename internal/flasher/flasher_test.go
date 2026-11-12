package flasher

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var testCtx = context.Background()

func runTest(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	b, e := exec.Command(name, args...).CombinedOutput()
	if e != nil {
		t.Fatalf("%s: %v: %s", name, e, b)
	}
	return b
}
func writeTest(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func makeDT(t *testing.T, extra string) []byte {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "source.dts")
	writeTest(t, p, []byte(`/dts-v1/;
/ { compatible="test,board"; qcom,board-id=<1 2>;
 serial-number="PRIVATE-SERIAL-SENTINEL";
 chosen { bootargs="PRIVATE-BOOTARGS-SENTINEL"; };
 gpio: pinctrl { gpio-controller; #gpio-cells=<2>; phandle=<1>;
 active: active { pins="gpio71"; function="gpio"; };
 sleep: sleep { pins="gpio71"; function="gpio"; };
 };
 leds { compatible="gpio-leds"; pinctrl-names="default", "sleep"; pinctrl-0=<&active>; pinctrl-1=<&sleep>;
 green { gpios=<&gpio 71 0>; }; };
 `+extra+` };`))
	out := filepath.Join(dir, "source.dtb")
	runTest(t, "dtc", "-f", "-q", "-I", "dts", "-O", "dtb", "-o", out, p)
	b, e := os.ReadFile(out)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func makeGPT(t *testing.T, f *os.File, total uint64, parts []Partition) {
	t.Helper()
	entries := make([]byte, 128*128)
	for i, p := range parts {
		b := entries[i*128 : (i+1)*128]
		b[0] = 0xa1
		b[16] = byte(i + 1)
		if p.GUID != "" {
			h := sha256.Sum256([]byte(p.GUID))
			copy(b[16:32], h[:16])
		}
		le.PutUint64(b[32:], p.Range.Offset/512)
		le.PutUint64(b[40:], (p.Range.Offset+p.Range.Length)/512-1)
		for j, c := range p.Name {
			le.PutUint16(b[56+j*2:], uint16(c))
		}
	}
	makeHeader := func(current, backup, array uint64) []byte {
		b := make([]byte, 512)
		copy(b, "EFI PART")
		le.PutUint32(b[8:], 0x10000)
		le.PutUint32(b[12:], 92)
		le.PutUint64(b[24:], current)
		le.PutUint64(b[32:], backup)
		le.PutUint64(b[40:], 34)
		le.PutUint64(b[48:], total-34)
		b[56] = 99
		le.PutUint64(b[72:], array)
		le.PutUint32(b[80:], 128)
		le.PutUint32(b[84:], 128)
		le.PutUint32(b[88:], crc32.ChecksumIEEE(entries))
		le.PutUint32(b[16:], crc32.ChecksumIEEE(b[:92]))
		return b
	}
	mbr := make([]byte, 512)
	mbr[450] = 0xee
	le.PutUint32(mbr[454:], 1)
	le.PutUint32(mbr[458:], uint32(total-1))
	mbr[510] = 0x55
	mbr[511] = 0xaa
	for _, x := range []struct {
		off uint64
		b   []byte
	}{{0, mbr}, {512, makeHeader(1, total-1, 2)}, {1024, entries}, {(total - 33) * 512, entries}, {(total - 1) * 512, makeHeader(total-1, 1, total-33)}} {
		if _, e := f.WriteAt(x.b, int64(x.off)); e != nil {
			t.Fatal(e)
		}
	}
}

type fixture struct {
	d                    *FileDevice
	report               HardwareReport
	root, mapper, images string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	t.Setenv("UBSAN_OPTIONS", "halt_on_error=1")
	dir := t.TempDir()
	paths := map[string]string{}
	for _, area := range []string{"user", "boot1", "boot2"} {
		p := filepath.Join(dir, area)
		f, e := os.Create(p)
		if e != nil {
			t.Fatal(e)
		}
		size := int64(4096)
		if area == "user" {
			size = 24 << 20
		}
		if e = f.Truncate(size); e != nil {
			t.Fatal(e)
		}
		if area == "user" {
			parts := []Partition{{Name: "boot", Range: Range{"user", 64 * 512, 256 << 10}}, {Name: "persist", Range: Range{"user", 1024 * 512, 16 << 10}}, {Name: "rootfs", Range: Range{"user", 2048 * 512, 16 << 20}}}
			makeGPT(t, f, uint64(size/512), parts)
			dt := makeDT(t, "")
			f.WriteAt(dt, 64*512)
			fs := filepath.Join(dir, "fs.ext4")
			ff, _ := os.Create(fs)
			ff.Truncate(16 << 20)
			ff.Close()
			runTest(t, "mke2fs", "-q", "-F", "-t", "ext4", "-b", "4096", "-E", "lazy_itable_init=0,lazy_journal_init=0", fs)
			payload := filepath.Join(dir, "payload")
			writeTest(t, payload, []byte("private fixture payload\n"))
			runTest(t, "debugfs", "-w", "-R", "write "+payload+" /state", fs)
			runTest(t, "debugfs", "-w", "-R", "set_inode_field /state mode 0100600", fs)
			runTest(t, "debugfs", "-w", "-R", "set_inode_field /state uid 123", fs)
			data, _ := os.ReadFile(fs)
			f.WriteAt(data, 2048*512)
		}
		f.Close()
		paths[area] = p
	}
	d, e := OpenFiles(paths, "fixture-unit", true)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	report, e := Inspect(testCtx, d, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Variants) != 1 || len(report.Issues) > 0 {
		t.Fatalf("report: %+v", report.Issues)
	}
	mapper := filepath.Join(dir, "extmap")
	flags := strings.Fields(string(runTest(t, "pkg-config", "--cflags", "--libs", "ext2fs")))
	args := []string{"-O1", "-Wall", "-Wextra", "-Werror", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-fno-omit-frame-pointer", "-fno-pie", "-no-pie", "../../scripts/native/extmap.c", "-o", mapper}
	args = append(args, flags...)
	runTest(t, "cc", args...)
	images := filepath.Join(dir, "images")
	os.Mkdir(images, 0700)
	writeTest(t, filepath.Join(images, "boot.img"), bytes.Repeat([]byte{0x88}, 4096))
	writeTest(t, filepath.Join(images, "rootfs.ext4"), bytes.Repeat([]byte{0x77}, 4096))
	return fixture{d, report, dir, mapper, images}
}
func qualifySimulator(r HardwareReport) HardwareProfile {
	p := HardwareProfile{Version: Schema, ID: "test-fixture", Status: "qualified", Qualified: true, KernelBuild: 17, Recipe: "simulator-v1", Subsystems: map[string]string{}, Bundle: map[string]string{}, RequiredAreas: []string{"user", "boot1", "boot2"}}
	for _, s := range requiredSubsystems {
		p.Subsystems[s] = "PASS"
	}
	for _, s := range []string{"kernel", "dtb", "modules", "mba", "mpss", "wcnss"} {
		p.Bundle[s] = strings.Repeat("a", 64)
	}
	for _, v := range r.Variants {
		p.DTBHashes = append(p.DTBHashes, v.SHA256)
		p.MapHashes = append(p.MapHashes, v.MapSHA256)
	}
	return p
}
func (tst fixture) backup(t *testing.T) string {
	t.Helper()
	p := filepath.Join(tst.root, "backup")
	_, e := CreateBackup(testCtx, tst.d, tst.report, p, BackupOptions{Mapper: tst.mapper, AllowFull: true})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestGPTAndDeviceBounds(t *testing.T) {
	f := newFixture(t)
	info, _ := f.d.Info(testCtx)
	if _, e := ReadGPT(testCtx, f.d, info); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"primary-crc", "backup-crc", "capacity", "guid", "overlap", "mirror"} {
		t.Run(kind, func(t *testing.T) {
			raw, _ := os.ReadFile(filepath.Join(f.root, "user"))
			switch kind {
			case "primary-crc":
				raw[530] ^= 1
			case "backup-crc":
				raw[len(raw)-512+18] ^= 1
			case "capacity":
				info.Geometry.Areas["user"] -= 512
				defer func() { info.Geometry.Areas["user"] += 512 }()
			case "guid", "overlap", "mirror":
				b := raw[1024 : 1024+16384]
				if kind == "guid" {
					copy(b[128+16:128+32], b[16:32])
				}
				if kind == "overlap" {
					le.PutUint64(b[128+32:], 64)
				}
				if kind == "mirror" {
					b[56] ^= 1
				}
				h := raw[512:1024]
				le.PutUint32(h[88:], crc32.ChecksumIEEE(b))
				clear(h[16:20])
				le.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))
			}
			p := filepath.Join(t.TempDir(), "bad")
			writeTest(t, p, raw)
			bad, e := OpenFiles(map[string]string{"user": p}, "bad", false)
			if e != nil {
				t.Fatal(e)
			}
			defer bad.Close()
			if _, e = ReadGPT(testCtx, bad, info); e == nil {
				t.Fatal("accepted corrupt GPT")
			}
		})
	}
	for _, r := range []Range{{"user", 0, 0}, {"user", 1, 512}, {"rpmb", 0, 512}, {"user", ^uint64(0) - 511, 1024}} {
		if e := f.d.Read(testCtx, r, io.Discard); e == nil {
			t.Fatal("out-of-range accepted")
		}
	}
}
func TestFDTAndProfileResolution(t *testing.T) {
	good := makeDT(t, "")
	v, e := ParseDTB(good, "boot", 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Issues) > 0 {
		t.Fatalf("sleep state caused false conflict: %v", v.Issues)
	}
	t.Run("disabled-parent", func(t *testing.T) {
		b := makeDT(t, `off { status="disabled"; child { gpios=<&gpio 71 0>; }; };`)
		v, e := ParseDTB(b, "boot", 0)
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range v.Resources {
			if r.Node == "/off/child" && r.Enabled {
				t.Fatal("disabled parent ignored")
			}
		}
		if len(v.Issues) > 0 {
			t.Fatal(v.Issues)
		}
	})
	t.Run("gpio-conflict", func(t *testing.T) {
		b := makeDT(t, `other { gpios=<&gpio 71 0>; };`)
		v, e := ParseDTB(b, "boot", 0)
		if e != nil {
			t.Fatal(e)
		}
		if len(v.Issues) == 0 {
			t.Fatal("no resource conflict")
		}
	})
	t.Run("dangling", func(t *testing.T) {
		b := makeDT(t, `other { gpios=<999 71 0>; };`)
		if _, e := ParseDTB(b, "boot", 0); e == nil {
			t.Fatal("dangling phandle accepted")
		}
	})
	for _, kind := range []string{"bounds", "token", "duplicate-phandle"} {
		t.Run(kind, func(t *testing.T) {
			b := append([]byte(nil), good...)
			switch kind {
			case "bounds":
				be.PutUint32(b[8:], ^uint32(0))
			case "token":
				be.PutUint32(b[be.Uint32(b[8:]):], 0xff)
			case "duplicate-phandle":
				b = makeDT(t, `dup { phandle=<1>; };`)
			}
			if _, e := ParseDTB(b, "bad", 0); e == nil {
				t.Fatal("corrupt FDT accepted")
			}
		})
	}
	r := HardwareReport{Version: Schema, Device: DeviceInfo{Identity{"simulator", "x"}, Geometry{512, map[string]uint64{"user": 100000, "boot1": 4096, "boot2": 4096}}}, Variants: []DTVariant{v}}
	p := qualifySimulator(r)
	if got := Resolve(r, []HardwareProfile{p}); got.Status != "matched" {
		t.Fatal(got)
	}
	q := p
	q.ID = "second"
	if got := Resolve(r, []HardwareProfile{p, q}); got.Status != "ambiguous" {
		t.Fatal(got)
	}
	p.Qualified = false
	if got := Resolve(r, []HardwareProfile{p}); got.Status == "matched" {
		t.Fatal(got)
	}
	changed, _ := ParseDTB(makeDT(t, `different { gpios=<&gpio 72 0>; };`), "boot", 0)
	r.Variants = append(r.Variants, changed)
	if got := Resolve(r, []HardwareProfile{qualifySimulator(HardwareReport{Variants: []DTVariant{v}})}); got.Status == "matched" {
		t.Fatal("same board-id bypassed different wiring")
	}
}
func TestCompactBackupAndDamagedGPTRestore(t *testing.T) {
	f := newFixture(t)
	backup := f.backup(t)
	m, e := LoadBackup(backup)
	if e != nil {
		t.Fatal(e)
	}
	if !m.FreeBlocksOmitted {
		t.Fatal("ext4 was not compact")
	}
	var saved, total uint64
	for _, r := range m.Coverage {
		total += r.Length
	}
	for _, x := range m.Extents {
		saved += x.Range.Length
	}
	if saved >= total {
		t.Fatal("did not omit free blocks")
	}
	if e = VerifyBackup(testCtx, backup, f.report.Device, f.mapper); e != nil {
		t.Fatal(e)
	}
	// Damage GPT and all saved extents. Restore uses physical addresses, not GPT.
	for _, x := range m.Extents {
		if e = f.d.Write(testCtx, x.Range, io.LimitReader(zeroReader{}, int64(x.Range.Length))); e != nil {
			t.Fatal(e)
		}
	}
	p, e := BuildRestorePlan(testCtx, f.d, backup, f.mapper)
	if e != nil {
		t.Fatal(e)
	}
	if e = Execute(testCtx, f.d, p, backup, backup, filepath.Join(f.root, "restore.jsonl"), Hash(p), nil, f.mapper); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadGPT(testCtx, f.d, f.report.Device); e != nil {
		t.Fatal(e)
	}
	rootRange := f.report.GPT.Partitions[2].Range
	data, e := ReadRange(testCtx, f.d, rootRange, 32<<20)
	if e != nil {
		t.Fatal(e)
	}
	restored := filepath.Join(f.root, "restored.ext4")
	writeTest(t, restored, data)
	runTest(t, "e2fsck", "-fn", restored)
	out := runTest(t, "debugfs", "-R", "stat /state", restored)
	if !bytes.Contains(out, []byte("Mode:  0600")) || !bytes.Contains(out, []byte("123")) {
		t.Fatalf("metadata lost: %s", out)
	}
	if out := runTest(t, "debugfs", "-R", "cat /state", restored); !bytes.Contains(out, []byte("private fixture payload")) {
		t.Fatal("contents lost")
	}
	foreign := f.report.Device
	foreign.Identity.Hardware = "different-unit"
	if e = VerifyBackup(testCtx, backup, foreign, f.mapper); e == nil {
		t.Fatal("foreign backup accepted")
	}
}

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) { clear(b); return len(b), nil }

type spyWriter struct {
	*FileDevice
	writes  int
	failAt  int
	corrupt bool
}

func (s *spyWriter) Write(ctx context.Context, r Range, src io.Reader) error {
	s.writes++
	if s.writes == s.failAt {
		partial := r
		partial.Length = 512
		if e := s.FileDevice.Write(ctx, partial, io.LimitReader(src, 512)); e != nil {
			return e
		}
		return errors.New("simulated disconnect")
	}
	if s.corrupt {
		return s.FileDevice.Write(ctx, r, io.LimitReader(zeroReader{}, int64(r.Length)))
	}
	return s.FileDevice.Write(ctx, r, src)
}
func TestExecutionGatesAndInterruptedWrites(t *testing.T) {
	for _, scenario := range []string{"cancel", "plan-changed", "image-corrupt", "source-changed", "backup-corrupt", "swapped", "write-1", "write-2", "readback", "success"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			backup := f.backup(t)
			profile := qualifySimulator(f.report)
			f.report.Resolution = Resolve(f.report, []HardwareProfile{profile})
			p, e := BuildInstallPlan(testCtx, f.d, f.report, f.images, "", backup, f.mapper)
			if e != nil {
				t.Fatal(e)
			}
			approved := Hash(p)
			spy := &spyWriter{FileDevice: f.d}
			ctx, cancel := context.WithCancel(testCtx)
			defer cancel()
			switch scenario {
			case "cancel":
				cancel()
			case "plan-changed":
				p.Operations[0].Range.Offset += 512
			case "image-corrupt":
				writeTest(t, filepath.Join(f.images, "boot.img"), make([]byte, 512))
			case "source-changed":
				f.d.Write(testCtx, p.Operations[0].Range, bytes.NewReader(bytes.Repeat([]byte{0x4a}, int(p.Operations[0].Range.Length))))
			case "backup-corrupt":
				writeTest(t, filepath.Join(backup, "extent-000000.bin"), []byte("bad"))
			case "swapped":
				f.d.info.Identity.Hardware = "changed"
			case "write-1":
				spy.failAt = 1
			case "write-2":
				spy.failAt = 2
			case "readback":
				spy.corrupt = true
			}
			e = Execute(ctx, spy, p, f.images, backup, filepath.Join(f.root, "install.jsonl"), approved, nil, f.mapper)
			if scenario == "success" {
				if e != nil {
					t.Fatal(e)
				}
				states, e := Analyze(testCtx, f.d, p)
				if e != nil {
					t.Fatal(e)
				}
				for _, state := range states {
					if state != "written" {
						t.Fatal(states)
					}
				}
				if e = Execute(ctx, spy, p, f.images, backup, filepath.Join(f.root, "install.jsonl"), approved, nil, f.mapper); e == nil {
					t.Fatal("silently resumed from journal")
				}
				return
			}
			if e == nil {
				t.Fatal("expected gate/error")
			}
			if scenario != "write-1" && scenario != "write-2" && scenario != "readback" && spy.writes != 0 {
				t.Fatal("wrote before preflight finished")
			}
			if strings.HasPrefix(scenario, "write-") {
				states, e := Analyze(testCtx, f.d, p)
				if e != nil {
					t.Fatal(e)
				}
				if len(states) != 2 {
					t.Fatal(states)
				}
				if states[spy.failAt-1] != "partial-or-changed" {
					t.Fatal("partial write was not detected", states)
				}
				restore, e := BuildRestorePlan(testCtx, f.d, backup, f.mapper)
				if e != nil {
					t.Fatal(e)
				}
				if e = Execute(testCtx, f.d, restore, backup, backup, filepath.Join(f.root, "recovery.jsonl"), Hash(restore), nil, f.mapper); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
func TestQuickDefaultRefusalAndSpace(t *testing.T) {
	for _, answer := range []string{"", "\n", "n", "yes", "да"} {
		if ConfirmNoBackup(answer) {
			t.Fatalf("accepted %q", answer)
		}
	}
	if !ConfirmNoBackup("y") || !ConfirmNoBackup("Y") {
		t.Fatal("explicit yes rejected")
	}
	f := newFixture(t)
	spy := &spyWriter{FileDevice: f.d}
	p := filepath.Join(f.root, "no-space")
	_, e := CreateBackup(testCtx, spy, f.report, p, BackupOptions{Mapper: f.mapper, AllowFull: true, CheckSpace: func(string, uint64) error { return errors.New("no space") }})
	if e == nil || spy.writes != 0 {
		t.Fatal("space gate failed")
	}
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("partial backup published")
	}
	r := f.report
	r.Resolution = Resolve(r, []HardwareProfile{qualifySimulator(r)})
	plan, e := BuildInstallPlan(testCtx, spy, r, f.images, "y", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = Execute(testCtx, spy, plan, f.images, "", filepath.Join(f.root, "quick.jsonl"), Hash(plan), nil); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(f.root, "quick.jsonl"))
	if !bytes.Contains(b, []byte(`"backup_waived":true`)) {
		t.Fatal("waiver not journaled")
	}
}
func TestUnsupportedAutomaticallyExportsSanitizedArchive(t *testing.T) {
	f := newFixture(t)
	result, e := InspectAndReport(testCtx, f.d, nil, filepath.Join(f.root, "reports"), nil)
	if e != nil {
		t.Fatal(e)
	}
	if result.Archive == "" || !strings.HasPrefix(filepath.Base(result.Archive), "board-report-") {
		t.Fatal("missing automatic archive")
	}
	file, e := os.Open(result.Archive)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	gz, e := gzip.NewReader(file)
	if e != nil {
		t.Fatal(e)
	}
	tr := tar.NewReader(gz)
	found := map[string][]byte{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{"PRIVATE-SERIAL-SENTINEL", "PRIVATE-BOOTARGS-SENTINEL", "fixture-unit", "private fixture payload"} {
			if bytes.Contains(b, []byte(secret)) {
				t.Fatalf("archive leaked %s in %s", secret, h.Name)
			}
		}
		if strings.Contains(h.Name, "..") || filepath.IsAbs(h.Name) {
			t.Fatal("unsafe tar path")
		}
		found[h.Name] = b
	}
	for _, name := range []string{"HardwareReport.json", "SOURCES.json", "PARTITION-HASHES.json", "stock/variant-000.dtb", "stock/variant-000.dts", "SHA256SUMS"} {
		if len(found[name]) == 0 {
			t.Fatal("missing", name)
		}
	}
	var report HardwareReport
	if e = json.Unmarshal(found["HardwareReport.json"], &report); e != nil {
		t.Fatal(e)
	}
	if report.Device.Identity.Hardware != "" || report.GPT.DiskGUID != "" {
		t.Fatal("private identity in report")
	}
	if _, e := parseFDT(found["stock/variant-000.dtb"]); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(SupportMessage(result.Archive), result.Archive) {
		t.Fatal("UI omits archive link")
	}
}
func TestImageContainers(t *testing.T) {
	raw := makeDT(t, "")
	for _, kind := range []string{"raw", "android", "dtbo", "qcdt"} {
		t.Run(kind, func(t *testing.T) {
			b := raw
			switch kind {
			case "android":
				b = make([]byte, 2048)
				copy(b, "ANDROID!")
				le.PutUint32(b[8:], uint32(len(raw)))
				le.PutUint32(b[36:], 2048)
				b = append(b, raw...)
				b = append(b, make([]byte, (2048-len(raw)%2048)%2048)...)
			case "dtbo":
				b = make([]byte, 64)
				be.PutUint32(b, 0xd7b7ab1e)
				be.PutUint32(b[4:], uint32(64+len(raw)))
				be.PutUint32(b[8:], 32)
				be.PutUint32(b[12:], 32)
				be.PutUint32(b[16:], 1)
				be.PutUint32(b[20:], 32)
				be.PutUint32(b[32:], uint32(len(raw)))
				be.PutUint32(b[36:], 64)
				b = append(b, raw...)
			case "qcdt":
				b = make([]byte, 36)
				copy(b, "QCDT")
				le.PutUint32(b[4:], 2)
				le.PutUint32(b[8:], 1)
				le.PutUint32(b[28:], 36)
				le.PutUint32(b[32:], uint32(len(raw)))
				b = append(b, raw...)
			}
			vs, e := DTBs(b, kind)
			if e != nil || len(vs) != 1 {
				t.Fatal(e, len(vs))
			}
			if len(b) > 40 {
				bad := append([]byte(nil), b...)
				_ = binary.BigEndian
				_, _ = DTBs(bad[:40], kind)
			}
		})
	}
}

func TestCompactBackupRejectsOmittedUsedBlocks(t *testing.T) {
	f := newFixture(t)
	backup := f.backup(t)
	m, e := LoadBackup(backup)
	if e != nil {
		t.Fatal(e)
	}
	var original Range
	found := false
	for i := range m.Regions {
		r := &m.Regions[i]
		if r.Mode == "ext4-allocated" && len(r.Allocated) > 0 {
			original = r.Allocated[len(r.Allocated)-1]
			r.Allocated[len(r.Allocated)-1].Length -= 512
			found = true
			break
		}
	}
	if !found {
		t.Fatal("fixture did not create an allocation map")
	}
	kept := []Extent{}
	for _, x := range m.Extents {
		if x.Range == original {
			x.Range.Length -= 512
			p := filepath.Join(backup, x.File)
			if e = os.Truncate(p, int64(x.Range.Length)); e != nil {
				t.Fatal(e)
			}
			x.SHA256, e = HashFile(p)
			if e != nil {
				t.Fatal(e)
			}
		}
		kept = append(kept, x)
	}
	m.Extents = kept
	if e = SaveJSON(filepath.Join(backup, "BackupManifest.json"), m); e != nil {
		t.Fatal(e)
	}
	if e = VerifyBackup(testCtx, backup, f.report.Device, f.mapper); e == nil {
		t.Fatal("self-declared coverage hid an omitted occupied extent")
	}
}
func TestDirtyExt4UsesFullPartitionFallback(t *testing.T) {
	f := newFixture(t)
	r := f.report.GPT.Partitions[2].Range
	block, e := ReadRange(testCtx, f.d, Range{r.Area, r.Offset + 1024, 512}, 512)
	if e != nil {
		t.Fatal(e)
	}
	le.PutUint16(block[58:], 0)
	if e = f.d.Write(testCtx, Range{r.Area, r.Offset + 1024, 512}, bytes.NewReader(block)); e != nil {
		t.Fatal(e)
	}
	backup := filepath.Join(f.root, "dirty-backup")
	m, e := CreateBackup(testCtx, f.d, f.report, backup, BackupOptions{Mapper: f.mapper, AllowFull: true})
	if e != nil {
		t.Fatal(e)
	}
	if len(m.Defects) == 0 {
		t.Fatal("missing full-copy defect note")
	}
	for _, region := range m.Regions {
		if region.Range == r && region.Mode != "full" {
			t.Fatal("dirty filesystem copied compactly")
		}
	}
}
func TestVendorBootDTBsAndBounds(t *testing.T) {
	raw := makeDT(t, "")
	b := make([]byte, 4096)
	copy(b, "VNDRBOOT")
	le.PutUint32(b[8:], 3)
	le.PutUint32(b[12:], 4096)
	le.PutUint32(b[2096:], 2112)
	le.PutUint32(b[2100:], uint32(len(raw)))
	b = append(b, raw...)
	vs, e := DTBs(b, "vendor_boot")
	if e != nil || len(vs) != 1 || vs[0].Offset != 4096 {
		t.Fatal(e, vs)
	}
	le.PutUint32(b[2100:], ^uint32(0))
	if _, e = DTBs(b, "vendor_boot"); e == nil {
		t.Fatal("invalid vendor_boot bounds accepted")
	}
}
func FuzzDTBContainers(f *testing.F) {
	f.Add([]byte("ANDROID!"))
	f.Add([]byte{0xd0, 0x0d, 0xfe, 0xed})
	f.Add(make([]byte, 40))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 1<<20 {
			return
		}
		_, _ = DTBs(b, "fuzz")
	})
}

func TestOwnLED17CandidatePreservesSIMAndOtherProperties(t *testing.T) {
	workspace := os.Getenv("MICROSTICK_WORKSPACE")
	if !filepath.IsAbs(workspace) {
		t.Fatal("set external MICROSTICK_WORKSPACE for exact #17 candidate verification")
	}
	source := filepath.Join(workspace, "hardware/current/device-tree/boot-image.dtb")
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	out, e := BuildOwnLED17(raw)
	if e != nil {
		t.Fatal(e)
	}
	old, e := parseFDT(raw)
	if e != nil {
		t.Fatal(e)
	}
	updated, e := parseFDT(out)
	if e != nil {
		t.Fatal(e)
	}
	if len(old) != len(updated) {
		t.Fatal("nodes changed")
	}
	changes := 0
	for i, n := range old {
		if n.path != updated[i].path || len(n.props) != len(updated[i].props) {
			t.Fatal("unexpected node/property change")
		}
		for key, b := range n.props {
			after := updated[i].props[key]
			if bytes.Equal(b, after) {
				continue
			}
			allowed := key == "gpios" && (n.path == "/leds/wifi" || n.path == "/leds/internet" || n.path == "/leds/os") || key == "pins" && filepath.Base(n.path) == "msmgpio-leds"
			if !allowed {
				t.Fatal("modified SIM or unrelated hardware property", n.path, key)
			}
			changes++
		}
	}
	if changes != 4 {
		t.Fatal("incomplete candidate", changes)
	}
	v, e := ParseDTB(out, "candidate", 0)
	if e != nil || len(v.Issues) != 0 {
		t.Fatal("candidate conflict", e)
	}
	want := map[string]uint32{"/leds/wifi": 73, "/leds/internet": 71, "/leds/os": 72}
	for _, r := range v.Resources {
		if pin, ok := want[r.Node]; ok && r.Property == "gpios" {
			if len(r.Cells) != 2 || r.Cells[0] != pin || r.Cells[1] != 0 {
				t.Fatal("wrong RGB wiring")
			}
		}
	}
	after, e := os.ReadFile(source)
	if e != nil || !bytes.Equal(raw, after) {
		t.Fatal("canonical hardware input changed")
	}
}

func TestQCDTUniqueVariantsAndSharedSelectors(t *testing.T) {
	const count = 72
	data := make([]byte, 4096)
	copy(data, "QCDT")
	le.PutUint32(data[4:], 3)
	le.PutUint32(data[8:], count)
	variants := make([][]byte, 23)
	offsets := make([]uint32, 23)
	for i := range variants {
		variants[i] = makeDT(t, fmt.Sprintf("variant { example-id=<%d>; };", i))
		offsets[i] = uint32(len(data))
		data = append(data, variants[i]...)
		for len(data)%2048 != 0 {
			data = append(data, 0)
		}
	}
	for i := 0; i < count; i++ {
		entry := data[12+i*40 : 12+(i+1)*40]
		le.PutUint32(entry, uint32(200+i))
		le.PutUint32(entry[32:], offsets[i%23])
		le.PutUint32(entry[36:], uint32((len(variants[i%23])+2047)/2048*2048))
	}
	parsed, e := DTBs(data, "qcdt")
	if e != nil {
		t.Fatal(e)
	}
	if len(parsed) != 23 {
		t.Fatal("shared QCDT payloads were duplicated", len(parsed))
	}
	selectors := 0
	for i, v := range parsed {
		selectors += len(v.Selectors)
		if v.SHA256 != HashBytes(variants[i]) || v.Offset != uint64(offsets[i]) {
			t.Fatal("QCDT source offsets/hashes changed")
		}
	}
	if selectors != count {
		t.Fatal("container selectors lost", selectors)
	}
}
