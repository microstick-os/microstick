package flasher

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readReportArchive(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if h.Typeflag != tar.TypeReg || h.Mode != 0600 || filepath.IsAbs(h.Name) || strings.Contains(h.Name, "..") || files[h.Name] != nil {
			t.Fatal("invalid archive member", h.Name)
		}
		files[h.Name], e = io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{"PRIVATE-", "fixture-unit", "private fixture payload"} {
			if bytes.Contains(files[h.Name], []byte(secret)) {
				t.Fatalf("private data in %s", h.Name)
			}
		}
	}
	lines := strings.Split(strings.TrimSpace(string(files["SHA256SUMS"])), "\n")
	if len(lines) != len(files)-1 {
		t.Fatal("manifest coverage differs")
	}
	seen := map[string]bool{}
	for _, line := range lines {
		h, name, ok := strings.Cut(line, "  ")
		if !ok || seen[name] || files[name] == nil || HashBytes(files[name]) != h {
			t.Fatal("invalid manifest entry", name)
		}
		seen[name] = true
	}
	return files
}

func decodeReportFile(t *testing.T, files map[string][]byte, name string, value any) {
	t.Helper()
	if e := json.Unmarshal(files[name], value); e != nil {
		t.Fatal(name, e)
	}
}

func reportQCDT(raw, other []byte) []byte {
	const table = 12 + 3*40
	b := make([]byte, table)
	copy(b, "QCDT")
	le.PutUint32(b[4:], 3)
	le.PutUint32(b[8:], 3)
	for i := 0; i < 3; i++ {
		p := b[12+i*40:]
		le.PutUint32(p, uint32(206+i))
		le.PutUint32(p[4:], 8)
		le.PutUint32(p[8:], 256)
		off, n := table, len(raw)
		if i == 2 {
			off, n = table+len(raw), len(other)
		}
		le.PutUint32(p[32:], uint32(off))
		le.PutUint32(p[36:], uint32(n))
	}
	return append(append(b, raw...), other...)
}

func TestBoardReportReconstructsRegistrySourceSet(t *testing.T) {
	f := newFixture(t)
	raw := makeDT(t, `wiring { qcom,exvbus_gpio=<&gpio 121 0>; qcom,gpio-err-ready=<&gpio 69 0>; qcom,unknown-power-config=<1>; };`)
	other := makeDT(t, `other { gpios=<&gpio 72 0>; };`)
	boot := f.report.GPT.Partitions[0].Range
	b := make([]byte, boot.Length)
	copy(b, reportQCDT(raw, other))
	if e := f.d.Write(testCtx, boot, bytes.NewReader(b)); e != nil {
		t.Fatal(e)
	}
	r, e := Inspect(testCtx, f.d, nil, nil)
	if e != nil || len(r.Variants) != 2 || len(r.Variants[0].Selectors) != 2 {
		t.Fatal("QCDT inspection", e)
	}
	// Installation-only GUIDs must not enter the public hardware report.
	r.BootFirmware = &BootFirmware{TZ: Partition{GUID: "PRIVATE-TZ-GUID"}, HYP: Partition{GUID: "PRIVATE-HYP-GUID"}}
	archive, e := ExportBoardReport(testCtx, f.d, r, t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	files := readReportArchive(t, archive)
	var support BoardSupport
	var sources []DTSource
	var safe HardwareReport
	decodeReportFile(t, files, "SUPPORT.json", &support)
	decodeReportFile(t, files, "SOURCES.json", &sources)
	decodeReportFile(t, files, "HardwareReport.json", &safe)
	if !support.DTSourcesComplete || len(support.Missing) != 0 || support.SourceSetFingerprint != SourceSetFingerprint(r.Variants) || support.CompatibilitySetFingerprint != CompatibilitySetFingerprint(r.Variants) || support.StorageFingerprint != StorageFingerprint(r) {
		t.Fatal("original registry fingerprints missing", support)
	}
	if safe.BootFirmware != nil || safe.Device.Identity != (Identity{}) || safe.GPT.DiskGUID != "" {
		t.Fatal("installation identity leaked")
	}
	if len(sources) != len(r.Variants) || len(safe.Variants) != len(r.Variants) {
		t.Fatal("variant set differs")
	}
	for i, src := range sources {
		v := r.Variants[i]
		if !src.InSourceSet || src.OriginalSHA256 != v.SHA256 || src.OriginalMapSHA256 != v.MapSHA256 || src.OriginalCompatibilitySHA256 != v.CompatibilitySHA256 || !reflect.DeepEqual(src.Selectors, v.Selectors) || !reflect.DeepEqual(src.BoardID, v.BoardID) || !reflect.DeepEqual(safe.Variants[i].Selectors, v.Selectors) {
			t.Fatal("source metadata lost", i)
		}
	}
	if sources[0].OriginalMapSHA256 == safe.Variants[0].MapSHA256 || sources[0].OriginalCompatibilitySHA256 == safe.Variants[0].CompatibilitySHA256 {
		t.Fatal("fixture must distinguish original and sanitized maps")
	}
	nodes, e := parseFDT(files["stock/variant-000.dtb"])
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range nodes {
		if n.path == "/wiring" && (len(n.props["qcom,exvbus_gpio"]) != 12 || len(n.props["qcom,gpio-err-ready"]) != 12) {
			t.Fatal("downstream wiring omitted")
		}
	}

	// Board-specific inputs below come only from the exported archive. Runtime
	// bundle and installation requirements come from the existing registry.
	payload, id, key, keys := registryFixture(t)
	variant := payload.Boards[0].Variants[0]
	profile := &variant.Profile
	profile.ID, profile.Board, profile.Layout, profile.Recipe = "synthetic-report-linux17", "synthetic-report", "boot-rootfs", "preserve-gpt-v1"
	profile.LayoutSHA256 = support.StorageFingerprint
	profile.SourceSets = []ReviewedSourceSet{{ID: "factory-qcdt", Fingerprint: support.SourceSetFingerprint, CompatibilityFingerprint: support.CompatibilitySetFingerprint, Review: "synthetic hardware map"}}
	profile.DTBHashes, profile.MapHashes = nil, nil
	for _, src := range sources {
		profile.DTBHashes = append(profile.DTBHashes, src.OriginalSHA256)
		profile.MapHashes = append(profile.MapHashes, src.OriginalMapSHA256)
	}
	payload.Boards = append(payload.Boards, BoardDescriptor{Version: 1, ID: profile.Board, Name: "Synthetic report board", SoC: "msm8916", Revision: 1, Variants: []BoardVariant{variant}})
	snapshot, e := VerifyRegistry(signedFixture(t, payload, id, key), keys)
	if e != nil {
		t.Fatal(e)
	}
	profiles := snapshot.Profiles(EngineVersion, "1.0.0", nil)
	if got := Resolve(r, profiles); got.Status != "matched" || got.Profile != profile.ID {
		t.Fatal("archive-derived registry rejected source device", got)
	}
	for _, kind := range []string{"gpio", "vendor-property", "selector", "extra", "missing"} {
		t.Run(kind, func(t *testing.T) {
			rr := r
			rr.Variants = append([]DTVariant(nil), r.Variants...)
			switch kind {
			case "gpio", "vendor-property":
				extra := `wiring { qcom,exvbus_gpio=<&gpio 121 0>; qcom,gpio-err-ready=<&gpio 69 0>; qcom,unknown-power-config=<2>; };`
				if kind == "gpio" {
					extra = `wiring { qcom,exvbus_gpio=<&gpio 120 0>; qcom,gpio-err-ready=<&gpio 69 0>; qcom,unknown-power-config=<1>; };`
				}
				v, e := ParseDTB(makeDT(t, extra), "boot", rr.Variants[0].Offset)
				if e != nil {
					t.Fatal(e)
				}
				v.Selectors = rr.Variants[0].Selectors
				rr.Variants[0] = v
			case "selector":
				rr.Variants[0].Selectors = [][]uint32{{999}}
			case "extra":
				rr.Variants = append(rr.Variants, rr.Variants[0])
				// A distinct selector must not be mistaken for a duplicate alias.
				rr.Variants[len(rr.Variants)-1].Selectors = [][]uint32{{999}}
			case "missing":
				rr.Variants = rr.Variants[:1]
			}
			if Resolve(rr, profiles).Status == "matched" {
				t.Fatal("changed hardware/source set accepted")
			}
		})
	}
}

type reportDevice struct {
	Reader
	security               string
	fail                   Range
	reads                  []Range
	probes, writes, resets int
}

func (d *reportDevice) Read(ctx context.Context, r Range, w io.Writer) error {
	d.reads = append(d.reads, r)
	if r == d.fail {
		return errors.New("PRIVATE-READ-DIAGNOSTIC")
	}
	return d.Reader.Read(ctx, r, w)
}
func (d *reportDevice) SecureBoot(context.Context) (string, error) {
	d.probes++
	if d.security == "error" {
		return "", errors.New("PRIVATE-SECURITY-DIAGNOSTIC")
	}
	return d.security, nil
}
func (d *reportDevice) Write(context.Context, Range, io.Reader) error {
	d.writes++
	return errors.New("unexpected write")
}
func (d *reportDevice) Reset(context.Context) error {
	d.resets++
	return errors.New("unexpected reset")
}

func TestBoardReportBootDiagnostics(t *testing.T) {
	for _, kind := range []string{"legacy", "modern", "mixed", "enabled", "unknown-security", "invalid-security", "unavailable-security", "missing", "duplicate", "corrupt", "unknown-abi", "unknown-map", "oversize", "read-failed"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			tz, hyp := 32, 32
			if kind == "modern" {
				tz, hyp = 64, 64
			} else if kind == "mixed" {
				hyp = 64
			}
			r := addTestFirmware(t, f, tz, hyp, 0)
			d := &reportDevice{Reader: f.d, security: "disabled"}
			tzPart := r.GPT.Partitions[len(r.GPT.Partitions)-2]
			want := "compatible"
			switch kind {
			case "enabled":
				d.security = "enabled"
			case "unknown-security":
				d.security = "error"
			case "invalid-security":
				d.security = "PRIVATE-INVALID-SECURITY"
			case "missing":
				r.GPT.Partitions = r.GPT.Partitions[:len(r.GPT.Partitions)-2]
				want = "missing"
			case "duplicate":
				r.GPT.Partitions = append(r.GPT.Partitions, tzPart)
				want = "duplicate-partition"
			case "oversize":
				r.GPT.Partitions[len(r.GPT.Partitions)-2].Range.Length = 9 << 20
				want = "invalid-range"
			case "read-failed":
				d.fail = tzPart.Range
				want = "read-failed"
			case "corrupt", "unknown-abi", "unknown-map":
				b := make([]byte, tzPart.Range.Length)
				copy(b, testFirmware(32, true))
				if kind == "corrupt" {
					b[0] = 0
					want = "invalid-elf"
				} else if kind == "unknown-abi" {
					clear(b[512:550])
					want = "unsupported-abi"
				} else {
					le.PutUint32(b[24:], 0x12345678)
					want = "unsupported-abi"
				}
				copy(b[1024:], "PRIVATE-FIRMWARE-SENTINEL")
				if e := f.d.Write(testCtx, tzPart.Range, bytes.NewReader(b)); e != nil {
					t.Fatal(e)
				}
			}
			var reader Reader = d
			if kind == "unavailable-security" {
				reader = struct{ Reader }{d}
			}
			archive, e := ExportBoardReport(testCtx, reader, r, t.TempDir(), nil)
			if e != nil {
				t.Fatal(e)
			}
			files := readReportArchive(t, archive)
			var support BoardSupport
			decodeReportFile(t, files, "SUPPORT.json", &support)
			got := support.BootFirmware
			if got.TZ.Status != want || !support.DTSourcesComplete {
				t.Fatalf("diagnostics: %+v", support)
			}
			security := "unavailable"
			if (want == "compatible" || kind == "unknown-abi") && kind != "unknown-security" && kind != "invalid-security" && kind != "unavailable-security" {
				security = d.security
			}
			if got.SecureBoot != security || d.writes != 0 || d.resets != 0 {
				t.Fatal("invalid security or write/reset during export", got)
			}
			probeStatus, probes := "skipped-memory-map", 0
			if want == "compatible" || kind == "unknown-abi" {
				probeStatus, probes = "performed", 1
				if kind == "unknown-security" {
					probeStatus = "read-failed"
				} else if kind == "invalid-security" {
					probeStatus = "invalid-response"
				} else if kind == "unavailable-security" {
					probeStatus, probes = "unavailable-reader", 0
				}
			}
			if got.SecureBootStatus != probeStatus || d.probes != probes {
				t.Fatal("probe status differs", got.SecureBootStatus, d.probes)
			}
			if want == "compatible" && (got.TZ.ELFClass != tz || got.HYP.ELFClass != hyp || got.TZ.Entry != 0x86500000 || len(got.TZ.Loads) != 1 || got.TZ.Loads[0].Physical != 0x86500000 || requireHash(got.TZ.SHA256) != nil) {
				t.Fatal("ELF metadata missing", got)
			}
			for _, read := range d.reads {
				allowed := false
				for _, p := range r.GPT.Partitions {
					if (p.Name == "boot" || p.Name == "tz" || p.Name == "hyp") && p.Range == read && read.Length <= 8<<20 {
						allowed = true
					}
				}
				if !allowed {
					t.Fatal("unexpected partition or unbounded read", read)
				}
			}
		})
	}
}

func TestBoardReportUnknownFirmwareRetainsDiagnostics(t *testing.T) {
	f := newFixture(t)
	r := addTestFirmware(t, f, 32, 32, 0)
	part := r.GPT.Partitions[len(r.GPT.Partitions)-2]
	b := make([]byte, part.Range.Length)
	copy(b, testFirmware(32, true))
	clear(b[512:550])
	const version = "TZ.XYZ.4.7-00321-M8916-1.0"
	copy(b[512:], "QC_IMAGE_VERSION_STRING="+version)
	copy(b[1024:], "OEM_IMAGE_VERSION_STRING=PRIVATE-OEM-SENTINEL")
	d := &reportDevice{Reader: f.d, security: "disabled"}
	if e := f.d.Write(testCtx, part.Range, bytes.NewReader(b)); e != nil {
		t.Fatal(e)
	}
	archive, e := ExportBoardReport(testCtx, d, r, t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	var support struct {
		Boot struct {
			TZ struct {
				Status    string   `json:"status"`
				Versions  []string `json:"version_strings"`
				MemoryMap bool     `json:"memory_map_compatible"`
			} `json:"tz"`
			Security string `json:"secure_boot"`
			Probe    string `json:"secure_boot_status"`
		} `json:"boot_firmware"`
	}
	decodeReportFile(t, readReportArchive(t, archive), "SUPPORT.json", &support)
	if !reflect.DeepEqual(support.Boot.TZ.Versions, []string{version}) {
		t.Fatal("unknown firmware version lost", support.Boot.TZ.Versions)
	}
	if support.Boot.TZ.Status != "unsupported-abi" || !support.Boot.TZ.MemoryMap || support.Boot.Security != "disabled" || support.Boot.Probe != "performed" || d.probes != 1 || d.writes != 0 || d.resets != 0 {
		t.Fatal("unknown-family diagnostics incomplete", support)
	}
	// The unknown source is replaced; its name does not block the plan.
	before := d.probes
	fw, e := InspectBootFirmware(testCtx, d, r, BootFirmwarePolicy)
	if e != nil || !fw.Upgrade || d.probes != before+1 {
		t.Fatal("unknown source blocked replacement", e)
	}
}

func TestBoardReportFirmwareVersionIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       []string
	}{
		{"unknown-tz", "\x00TZ.BF.2.0-00385-M8916AAAAANAZR-1.0\x00", []string{"TZ.BF.2.0-00385-M8916AAAAANAZR-1.0"}},
		{"unknown-metadata", "QC_IMAGE_VERSION_STRING=SECURE.NEW.7.4-00012\x00", []string{"SECURE.NEW.7.4-00012"}},
		{"deduplicate", "TZ.BF.2.0\x00QC_IMAGE_VERSION_STRING=TZ.BF.2.0\x00", []string{"TZ.BF.2.0"}},
		{"oem-private", "OEM_IMAGE_VERSION_STRING=PRIVATE-OEM-SENTINEL\x00", nil},
		{"oem-version", "OEM_IMAGE_VERSION_STRING=TZ.BF.9.9-00123\x00", nil},
		{"private-token", "QC_IMAGE_VERSION_STRING=TZ.BF.2.0-PRIVATE-SENTINEL\x00", nil},
		{"oversize", "QC_IMAGE_VERSION_STRING=TZ.BF.2.0-" + strings.Repeat("A", 180) + "\x00", nil},
		{"embedded-prefix", "PRIVATETZ.BF.2.0\x00", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := firmwareVersionStrings([]byte(tc.data)); !reflect.DeepEqual(got, tc.want) {
				t.Fatal("version identifiers differ", got)
			}
		})
	}
}

func TestBoardReportIncompleteSourcesRemainDiagnostic(t *testing.T) {
	for _, kind := range []string{"read-failed", "selector-changed", "parse-failed"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			r := f.report
			d := &reportDevice{Reader: f.d}
			switch kind {
			case "read-failed":
				d.fail = r.GPT.Partitions[0].Range
			case "selector-changed":
				r.Variants = append([]DTVariant(nil), r.Variants...)
				r.Variants[0].Selectors = [][]uint32{{999}}
			case "parse-failed":
				r.Issues = []string{"PRIVATE-PARSER-DIAGNOSTIC"}
			}
			archive, e := ExportBoardReport(testCtx, d, r, t.TempDir(), nil)
			if e != nil {
				t.Fatal(e)
			}
			var support BoardSupport
			decodeReportFile(t, readReportArchive(t, archive), "SUPPORT.json", &support)
			if support.DTSourcesComplete || len(support.Missing) == 0 || support.SourceSetFingerprint != "" || support.CompatibilitySetFingerprint != "" {
				t.Fatal("incomplete source set has registry fingerprints", support)
			}
		})
	}
}

func TestBoardReportPreservesHardwareBindings(t *testing.T) {
	raw := makeDT(t, `intc: interrupt-controller { interrupt-controller; #interrupt-cells=<3>; };
 clock: clock { #clock-cells=<1>; }; regulator: regulator { regulator-min-microvolt=<1800000>; };
 reserved-memory { #address-cells=<1>; #size-cells=<1>; ranges; memory: region@1000 { reg=<0x1000 0x1000>; no-map; }; };
 iommu: iommu { #iommu-cells=<1>; };
 wiring { clocks=<&clock 1>; interrupt-parent=<&intc>; interrupts=<0 1 4>; vdd-supply=<&regulator>; memory-region=<&memory>; iommus=<&iommu 2>;
 qcom,gpio-stop-ack=<&gpio 70 0>; qcom,exvbus_gpio=<&gpio 121 0>; qcom,num-pins=<122>; qcom,pin-num=<1>; qcom,pin-type-gp;
 qcom,regulator-type=<1>; qcom,supply-min-voltage=<1800000>; active-clock-names="core"; proxy-clock-names="bus"; qcom,proxy-clock-names="xo"; clock-rate=<19200000>; power-domain=<1>; linux,memory-limit=<0x1000>; nidnt-gpio-polarity=<0>;
 qcom,unknown-private-config="PRIVATE-UNKNOWN-SENTINEL"; };
 buttons { compatible="gpio-keys"; button { gpios=<&gpio 37 0>; linux,code=<216>; }; };
 sim { gpios=<&gpio 0 0>; default-state="keep"; };
 usb { dr_mode="peripheral"; }; wifi { status="okay"; };
 `)
	safe, _, _, e := sanitizedDTB(raw)
	if e != nil {
		t.Fatal(e)
	}
	before, e := ParseDTB(raw, "boot", 0)
	if e != nil {
		t.Fatal(e)
	}
	after, e := ParseDTB(safe, "boot", 0)
	if e != nil || !reflect.DeepEqual(before.Resources, after.Resources) || !reflect.DeepEqual(before.Issues, after.Issues) {
		t.Fatal("hardware resource map differs", e)
	}
	originalNodes, _ := parseFDT(raw)
	safeNodes, _ := parseFDT(safe)
	for i, n := range originalNodes {
		for k, v := range n.props {
			if downstreamHardwareProperty.MatchString(k) && !bytes.Equal(v, safeNodes[i].props[k]) {
				t.Fatal("hardware binding omitted", n.path, k)
			}
		}
	}
	if bytes.Contains(safe, []byte("PRIVATE-")) {
		t.Fatal("private DT property retained")
	}
}

func TestBoardReportCancellationPublishesNoArchive(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(testCtx)
	cancel()
	if path, e := ExportBoardReport(ctx, f.d, f.report, dir, nil); e == nil || path != "" {
		t.Fatal("cancelled export succeeded")
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 0 {
		t.Fatal(fmt.Sprint("cancelled archive published: ", e))
	}
}
