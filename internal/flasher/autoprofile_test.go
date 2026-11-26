package flasher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func automaticTree(t *testing.T, downstream bool, pin uint32) []byte {
	t.Helper()
	gpio := "qcom,msm8916-pinctrl"
	usb := "qcom,ci-hdrc"
	emmc := "qcom,msm8916-sdhci"
	wifi := "qcom,wcn3620"
	modem := "qcom,msm8916-mss-pil"
	if downstream {
		gpio = "qcom,msm-gpio"
		usb = "qcom,hsusb-otg"
		emmc = "qcom,sdhci-msm"
		wifi = "qcom,wcnss_wlan"
		modem = "qcom,pil-q6v56-mss"
	}
	dts := `/dts-v1/; / { compatible="synthetic,unknown-model", "qcom,msm8916"; #address-cells=<1>;#size-cells=<1>;
 memory {device_type="memory";reg=<0x80000000 0x20000000>;};
 regulators {compatible="qcom,rpm-pm8916-regulators";
 vdd: l8 {phandle=<31>;regulator-name="8916_l8";regulator-min-microvolt=<2850000>;regulator-max-microvolt=<2950000>;};
 vio: l5 {phandle=<32>;regulator-name="8916_l5";regulator-min-microvolt=<1800000>;regulator-max-microvolt=<1800000>;};};
 gpio: pinctrl { compatible="` + gpio + `"; gpio-controller; #gpio-cells=<2>; phandle=<17>;
 ledpins: leds {pins="gpio20"; function="gpio";}; keypins: key {pins="gpio37";function="gpio";}; };
 usb {compatible="` + usb + `";reg=<0x078d9000 0x400>;};
 emmc {compatible="` + emmc + `";reg=<0x07824000 0x800>;bus-width=<8>;vdd-supply=<&vdd>;vdd-io-supply=<&vio>;vmmc-supply=<&vdd>;vqmmc-supply=<&vio>;};
 wifi {compatible="` + wifi + `";}; modem {compatible="` + modem + `";};
 spmi {compatible="qcom,spmi-pmic-arb";reg=<0x0200f000 0x1000>; pmic {compatible="qcom,pm8916";};};
 leds {compatible="gpio-leds";pinctrl-names="default";pinctrl-0=<&ledpins>;
 light {label="activity";gpios=<&gpio PIN 0>;default-state="off";};
 sim {label="sim:sel";gpios=<&gpio 2 0>;default-state="on";};};
 keys {compatible="gpio-keys";pinctrl-names="default";pinctrl-0=<&keypins>;
 reset {gpios=<&gpio 37 1>;linux,code=<0x198>;};};};`
	dts = strings.ReplaceAll(dts, "PIN", strings.TrimPrefix(fmtGPIO(pin), "gpio"))
	dir := t.TempDir()
	in := filepath.Join(dir, "source.dts")
	out := filepath.Join(dir, "source.dtb")
	writeTest(t, in, []byte(dts))
	runTest(t, "dtc", "-q", "-I", "dts", "-O", "dtb", "-o", out, in)
	b, e := os.ReadFile(out)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestAutomaticProfileUsesBindingsWithoutCatalog(t *testing.T) {
	f := newFixture(t)
	r := addTestFirmware(t, f, 32, 32, 0)
	r.GPT.Partitions = append(r.GPT.Partitions, Partition{Name: "modem", Range: Range{"user", 18 << 20, 128 << 10}})
	base := automaticTree(t, false, 20)
	active := automaticTree(t, true, 79)
	p, e := AutomaticProfile(r, AutomaticInputs{active, base, map[string]string{"kernel": HashBytes([]byte("kernel")), "modules": HashBytes([]byte("modules")), "mba": BootTZSHA256}})
	if e != nil {
		t.Fatal(e)
	}
	if got := Resolve(r, []HardwareProfile{p}); got.Status != "matched" || !canonicalNativeProfile(p) {
		t.Fatal(got)
	}
	target, e := p.SelectDTB(base)
	if e != nil {
		t.Fatal(e)
	}
	nodes, e := parseFDT(target)
	if e != nil {
		t.Fatal(e)
	}
	pins := map[uint32]bool{}
	for _, n := range nodes {
		if v, e := cells(n.props["gpios"]); e == nil && len(v) == 3 {
			pins[v[1]] = true
		}
	}
	if !pins[79] || !pins[2] || pins[20] {
		t.Fatal("source wiring or SIM state lost", pins)
	}
	inputs := p.AutomaticInputs()
	rebuilt, e := AutomaticProfile(r, *inputs)
	if e != nil || Hash(rebuilt) != Hash(p) {
		t.Fatal("retry cannot reconstruct profile", e)
	}
	p.Bundle["kernel"] = HashBytes(nil)
	if canonicalNativeProfile(p) {
		t.Fatal("edited automatic profile accepted")
	}
}
func TestAutomaticDTBUsesSpecificMissingBinding(t *testing.T) {
	base := automaticTree(t, false, 20)
	active := automaticTree(t, true, 79)
	for _, old := range []string{"qcom,msm8916", "qcom,hsusb-otg", "qcom,wcnss_wlan"} {
		changed := bytes.ReplaceAll(active, []byte(old), bytes.Repeat([]byte{'x'}, len(old)))
		if _, e := AdaptActiveDTB(changed, base); e == nil {
			t.Fatal("unknown component accepted", old)
		}
	}
	out, e := AdaptActiveDTB(active, base)
	if e != nil {
		t.Fatal(e)
	}
	nodes, e := parseFDT(out)
	if e != nil {
		t.Fatal(e)
	}
	props := map[string]map[string][]byte{}
	for _, n := range nodes {
		props[n.path] = n.props
	}
	rebuilt, e := DTBFromProperties(props)
	if e != nil {
		t.Fatal(e)
	}
	v, e := ParseDTB(rebuilt, "live", 0)
	if e != nil || len(v.Issues) > 0 {
		t.Fatal("live OF reconstruction failed", e, v.Issues)
	}
}

func TestAutomaticProfileBuildsAndExecutesWithoutCatalog(t *testing.T) {
	f := newFixture(t)
	r := addTestFirmware(t, f, 64, 64, 0)
	file, e := os.OpenFile(filepath.Join(f.root, "user"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	parts := append([]Partition(nil), r.GPT.Partitions...)
	parts = append(parts, Partition{Name: "modem", Range: Range{"user", 18 << 20, 128 << 10}})
	makeGPT(t, file, r.Device.Geometry.Areas["user"]/512, parts)
	file.Close()
	f.d.info.Identity.Kind = "storage"
	r.Device = f.d.info
	r.GPT, e = ReadGPT(testCtx, f.d, r.Device)
	if e != nil {
		t.Fatal(e)
	}
	base := automaticTree(t, false, 20)
	active := automaticTree(t, true, 79)
	profile, e := AutomaticProfile(r, AutomaticInputs{active, base, map[string]string{"kernel": HashBytes([]byte("kernel")), "modules": HashBytes([]byte("modules")), "mba": BootTZSHA256}})
	if e != nil {
		t.Fatal(e)
	}
	r.Resolution = Resolve(r, []HardwareProfile{profile})
	root, e := os.ReadFile(filepath.Join(f.root, "fs.ext4"))
	if e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(f.images, "rootfs.ext4"), root)
	if e = CreatePayload(testCtx, f.images, f.mapper); e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(f.images, "Preparation.json"), []byte("automatic preparation\n"))
	receipt, _ := HashFile(filepath.Join(f.images, "Preparation.json"))
	fw, e := InspectBootFirmware(testCtx, f.d, r, BootFirmwarePolicy)
	if e != nil {
		t.Fatal(e)
	}
	bundle := map[string]string{}
	for k, v := range profile.Bundle {
		bundle[k] = v
	}
	bundle["mpss"] = HashBytes([]byte("own-modem"))
	bundle["wcnss"] = HashBytes([]byte("own-wifi"))
	att := ImageAttestation{Version: Schema, Device: r.Device, Profile: profile.ID, Bundle: bundle, BootFirmware: fw, ReceiptSHA256: receipt, Images: map[string]string{}}
	for _, name := range []string{"boot.img", "rootfs.ext4", "Payload.json"} {
		att.Images[name], e = HashFile(filepath.Join(f.images, name))
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = SaveJSON(filepath.Join(f.images, "HardwareInputs.json"), att); e != nil {
		t.Fatal(e)
	}
	plan, e := BuildProfileInstallPlan(testCtx, f.d, r, profile, f.images, "y", "", f.mapper)
	if e != nil {
		t.Fatal(e)
	}
	if plan.Registry != nil {
		t.Fatal("automatic profile unexpectedly requires a catalog")
	}
	journal := filepath.Join(f.root, "automatic-install.jsonl")
	if e = Execute(testCtx, f.d, plan, f.images, "", journal, Hash(plan), nil, f.mapper); e != nil {
		t.Fatal(e)
	}
	rebuilt, e := AutomaticProfile(r, *profile.AutomaticInputs())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ResumePlan(testCtx, f.d, plan, rebuilt); e != nil {
		t.Fatal("automatic profile could not resume", e)
	}
}

func TestAutomaticInspectionUsesSufficientSourceOrReportsMissingBindings(t *testing.T) {
	f := newFixture(t)
	r := addTestFirmware(t, f, 64, 64, 0)
	file, e := os.OpenFile(filepath.Join(f.root, "user"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	parts := append([]Partition(nil), r.GPT.Partitions...)
	parts = append(parts, Partition{Name: "modem", Range: Range{"user", 18 << 20, 128 << 10}})
	makeGPT(t, file, r.Device.Geometry.Areas["user"]/512, parts)
	active := automaticTree(t, true, 79)
	base := automaticTree(t, false, 20)
	if _, e = file.WriteAt(active, int64(parts[0].Range.Offset)); e != nil {
		t.Fatal(e)
	}
	file.Close()
	detect := func(r HardwareReport) (HardwareProfile, error) {
		return AutomaticProfile(r, AutomaticInputs{r.ActiveDTB, base, map[string]string{"kernel": HashBytes([]byte("kernel")), "modules": HashBytes([]byte("modules")), "mba": BootTZSHA256}})
	}
	got, e := InspectWithActiveTree(testCtx, f.d, nil, nil, detect, t.TempDir(), nil)
	if e != nil || got.Archive != "" || got.Profile == nil || got.Report.ActiveOrigin != "source" {
		t.Fatal("sufficient source did not auto-profile", got.Report.Resolution, e)
	}
	unsupported := bytes.ReplaceAll(active, []byte("qcom,wcnss_wlan"), []byte("xxxx,wcnss_wlan"))
	got, e = InspectWithActiveTree(testCtx, f.d, nil, unsupported, detect, t.TempDir(), nil)
	if e != nil || got.Archive == "" || got.Profile != nil {
		t.Fatal("missing component did not produce diagnostic archive", e)
	}
	files := readReportArchive(t, got.Archive)
	var support BoardSupport
	decodeReportFile(t, files, "SUPPORT.json", &support)
	if len(support.RequiredInputs) == 0 || files["active.dtb"] == nil {
		t.Fatal("diagnostic archive omitted missing binding or active tree")
	}
}

func TestAutomaticMappingIgnoresUnrelatedVendorBindings(t *testing.T) {
	active := automaticTree(t, true, 79)
	base := automaticTree(t, false, 20)
	nodes, e := parseFDT(active)
	if e != nil {
		t.Fatal(e)
	}
	nodes = append(nodes, &dtNode{path: "/unused-vendor-device", parent: nodes[0], enabled: false, props: map[string][]byte{"status": []byte("disabled\x00"), "power-domains": cellBytes(0xfade)}})
	active, e = encodeFDT(active, nodes)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ParseDTB(active, "vendor", 0); e == nil {
		t.Fatal("fixture must exercise an unfamiliar vendor reference")
	}
	if e = ValidateActiveDTB(active); e != nil {
		t.Fatal("unrelated binding blocked collection", e)
	}
	if _, e = AdaptActiveDTB(active, base); e != nil {
		t.Fatal("unrelated binding blocked sufficient component mapping", e)
	}
}

func TestAutomaticMappingRequiresActualRadioBinding(t *testing.T) {
	active := automaticTree(t, true, 79)
	base := automaticTree(t, false, 20)
	nodes, e := parseFDT(active)
	if e != nil {
		t.Fatal(e)
	}
	nodes = append(nodes, &dtNode{path: "/different-radio", parent: nodes[0], enabled: true, props: map[string][]byte{"compatible": []byte("qcom,wcn3990\x00")}})
	active, e = encodeFDT(active, nodes)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = AdaptActiveDTB(active, base); e == nil || !strings.Contains(e.Error(), "qcom,wcn3990") {
		t.Fatal("different radio falsely mapped to common runtime", e)
	}
}

func automaticInspectionFixture(t *testing.T, active []byte, stock bool) (fixture, func(HardwareReport) (HardwareProfile, error)) {
	t.Helper()
	f := newFixture(t)
	r := addTestFirmware(t, f, 64, 64, 0)
	file, e := os.OpenFile(filepath.Join(f.root, "user"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	parts := append([]Partition(nil), r.GPT.Partitions...)
	parts = append(parts, Partition{Name: "modem", Range: Range{"user", 18 << 20, 128 << 10}})
	if stock {
		for i := range parts {
			if parts[i].Name == "rootfs" {
				parts[i].Name = "system"
			}
		}
		parts = append(parts, Partition{Name: "userdata", Range: Range{"user", 22 << 20, 512 << 10}})
	}
	makeGPT(t, file, r.Device.Geometry.Areas["user"]/512, parts)
	if _, e = file.WriteAt(active, int64(parts[0].Range.Offset)); e != nil {
		t.Fatal(e)
	}
	file.Close()
	base := automaticTree(t, false, 20)
	return f, func(r HardwareReport) (HardwareProfile, error) {
		return AutomaticProfile(r, AutomaticInputs{r.ActiveDTB, base, map[string]string{"kernel": HashBytes([]byte("kernel")), "modules": HashBytes([]byte("modules")), "mba": BootTZSHA256}})
	}
}

func TestAutomaticInspectionPrecedesMatchingCatalogAndItsRequirements(t *testing.T) {
	for _, stock := range []bool{false, true} {
		t.Run(fmt.Sprintf("stock=%t", stock), func(t *testing.T) {
			active := automaticTree(t, true, 79)
			f, detect := automaticInspectionFixture(t, active, stock)
			r, e := Inspect(testCtx, f.d, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			catalog := fixtureProfile(r)
			if Resolve(r, []HardwareProfile{catalog}).Status != "matched" {
				t.Fatal("catalog fixture does not match")
			}
			fallbackCalls := 0
			result, e := InspectWithFallback(testCtx, f.d, active, detect, func(context.Context) ([]HardwareProfile, error) {
				fallbackCalls++
				return nil, errors.New("catalog unavailable")
			}, t.TempDir(), nil)
			if e != nil || result.Profile == nil || result.Report.Resolution.Status != "matched" || fallbackCalls != 0 {
				t.Fatal("automatic path consulted catalog", result.Report.Resolution, e, fallbackCalls)
			}
			recipe := "preserve-gpt-v1"
			if stock {
				recipe = StockRecipe
			}
			if result.Profile.Recipe != recipe || result.Report.BootFirmware == nil || result.Profile.AutomaticInputs() == nil {
				t.Fatal("installation inputs missing", result.Profile)
			}
		})
	}
}

func TestAutomaticInspectionFallsBackOnlyForMissingBindings(t *testing.T) {
	active := automaticTree(t, true, 79)
	f, detect := automaticInspectionFixture(t, active, false)
	r, e := Inspect(testCtx, f.d, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	catalog := fixtureProfile(r)
	missing := bytes.ReplaceAll(active, []byte("qcom,wcnss_wlan"), []byte("xxxx,wcnss_wlan"))
	calls := 0
	fallback := func(context.Context) ([]HardwareProfile, error) { calls++; return []HardwareProfile{catalog}, nil }
	result, e := InspectWithFallback(testCtx, f.d, missing, detect, fallback, t.TempDir(), nil)
	if e != nil || result.Profile != nil || result.Report.Resolution.Profile != catalog.ID || result.Report.InputFailure != "" || calls != 1 {
		t.Fatal("fallback failed", e, result.Report.Resolution, calls)
	}
	for _, failure := range []error{context.Canceled, errors.New("input checksum mismatch"), &UnsupportedInput{Code: "boot-firmware-security", Message: "security refused"}} {
		calls = 0
		_, e = InspectWithFallback(testCtx, f.d, active, func(HardwareReport) (HardwareProfile, error) { return HardwareProfile{}, failure }, fallback, t.TempDir(), nil)
		if !errors.Is(e, failure) || calls != 0 {
			t.Fatal("blocking error fell back", failure, e, calls)
		}
	}
	different := bytes.ReplaceAll(active, []byte("qcom,msm8916"), []byte("qcom,msm8999"))
	calls = 0
	if _, e = InspectWithFallback(testCtx, f.d, different, detect, fallback, t.TempDir(), nil); e == nil || calls != 0 {
		t.Fatal("different platform fell back", e, calls)
	}
}

func TestAutomaticProfilesRetainDifferentBoardsAndReconstructInputs(t *testing.T) {
	for index, pin := range []uint32{65, 79, 91} {
		t.Run(fmt.Sprintf("board-%d", index), func(t *testing.T) {
			source := automaticTree(t, index != 2, pin)
			nodes, e := parseFDT(source)
			if e != nil {
				t.Fatal(e)
			}
			for _, n := range nodes {
				if string(n.props["device_type"]) == "memory\x00" {
					n.props["reg"] = cellBytes(0x80000000, uint32(256+index*256)<<20)
				}
				if compatibleNode(n, "qcom,sdhci-msm", "qcom,msm8916-sdhci") {
					n.props["bus-width"] = cellBytes(uint32(4 + index%2*4))
				}
				if n.path == "/" {
					n.props["qcom,board-id"] = cellBytes(uint32(10+index), uint32(index))
					n.props["model"] = []byte(fmt.Sprintf("Synthetic platform %d\x00", index))
				}
			}
			source, e = encodeFDT(source, nodes)
			if e != nil {
				t.Fatal(e)
			}
			f, detect := automaticInspectionFixture(t, source, index == 1)
			result, e := InspectWithFallback(testCtx, f.d, source, detect, nil, t.TempDir(), nil)
			if e != nil || result.Profile == nil {
				t.Fatal("profile not derived", e, result.Report.Resolution)
			}
			inputs := result.Profile.AutomaticInputs()
			rebuilt, e := AutomaticProfile(result.Report, *inputs)
			if e != nil || Hash(rebuilt) != Hash(*result.Profile) {
				t.Fatal("profile cannot reconstruct", e)
			}
			target, e := rebuilt.SelectDTB(inputs.BaseDTB)
			if e != nil {
				t.Fatal(e)
			}
			mapped, e := parseFDT(target)
			if e != nil {
				t.Fatal(e)
			}
			sawPin, sawRAM, sawWidth := false, false, false
			for _, n := range mapped {
				if v, _ := cells(n.props["gpios"]); len(v) == 3 && v[1] == pin {
					sawPin = true
				}
				if string(n.props["device_type"]) == "memory\x00" {
					v, _ := cells(n.props["reg"])
					sawRAM = len(v) > 0 && v[len(v)-1] == uint32(256+index*256)<<20
				}
				if compatibleNode(n, "qcom,msm8916-sdhci") {
					v, _ := cells(n.props["bus-width"])
					sawWidth = len(v) == 1 && v[0] == uint32(4+index%2*4)
				}
			}
			if !sawPin || !sawRAM || !sawWidth {
				t.Fatal("board inputs not retained", sawPin, sawRAM, sawWidth)
			}
		})
	}
}

func TestInspectionUsesLocalCatalogWithoutRefreshing(t *testing.T) {
	active := automaticTree(t, true, 79)
	f, _ := automaticInspectionFixture(t, active, false)
	report, e := Inspect(testCtx, f.d, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	local := fixtureProfile(report)
	calls := 0
	got, e := InspectWithOptions(testCtx, f.d, InspectionOptions{
		ActiveDTB: active,
		Detect: func(HardwareReport) (HardwareProfile, error) {
			return HardwareProfile{}, &UnsupportedInput{Code: "active-dtb-binding", Message: "unsupported synthetic component"}
		},
		Profiles: []HardwareProfile{local},
		Refresh: func(context.Context) ([]HardwareProfile, error) {
			calls++
			return nil, errors.New("unexpected network")
		},
		RequireResearch: true,
	}, t.TempDir(), nil)
	if e != nil || got.Report.Resolution.Profile != local.ID || calls != 0 {
		t.Fatal("local profile caused network access", e, got.Report.Resolution, calls)
	}
}

func TestUnknownDeviceRequestsResearchBeforeRegistry(t *testing.T) {
	f := newFixture(t)
	file, e := os.OpenFile(filepath.Join(f.root, "user"), os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	part := f.report.GPT.Partitions[0]
	if _, e = file.WriteAt(make([]byte, part.Range.Length), int64(part.Range.Offset)); e != nil {
		t.Fatal(e)
	}
	file.Close()
	calls := 0
	got, e := InspectWithOptions(testCtx, f.d, InspectionOptions{
		Detect: func(HardwareReport) (HardwareProfile, error) {
			t.Error("detection without source")
			return HardwareProfile{}, nil
		},
		RequireResearch: true,
		Refresh:         func(context.Context) ([]HardwareProfile, error) { calls++; return nil, nil },
	}, t.TempDir(), nil)
	var needed *ResearchRequired
	if !errors.As(e, &needed) || calls != 0 || got.Archive != "" {
		t.Fatal("unknown device skipped automatic research", e, calls, got.Archive)
	}
}

func TestRegistryRefreshOccursOnlyAfterCompleteResearchAndLocalMiss(t *testing.T) {
	active := automaticTree(t, true, 79)
	f, _ := automaticInspectionFixture(t, active, false)
	report, e := Inspect(testCtx, f.d, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	local := fixtureProfile(report)
	calls := 0
	got, e := InspectWithOptions(testCtx, f.d, InspectionOptions{
		ResolveActive: func(HardwareReport) ([]byte, error) { return active, nil },
		Detect: func(HardwareReport) (HardwareProfile, error) {
			return HardwareProfile{}, &UnsupportedInput{Code: "active-dtb-binding", Message: "missing component rule"}
		},
		RequireResearch: true,
		Refresh:         func(context.Context) ([]HardwareProfile, error) { calls++; return []HardwareProfile{local}, nil },
	}, t.TempDir(), nil)
	if e != nil || got.Report.Resolution.Profile != local.ID || calls != 1 {
		t.Fatal("necessary fallback failed", e, got.Report.Resolution, calls)
	}
}
