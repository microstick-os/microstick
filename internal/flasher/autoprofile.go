package flasher

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// AutomaticInputs contains the component inputs needed to reconstruct a device
// profile. Operation records keep these inputs alongside the prepared images.
type AutomaticInputs struct {
	ActiveDTB []byte            `json:"active_dtb"`
	BaseDTB   []byte            `json:"base_dtb"`
	Bundle    map[string]string `json:"bundle_sha256"`
}
type automaticProfile struct {
	inputs                           AutomaticInputs
	digest, device, storage, sources string
	target                           []byte
}

func compatibleNode(n *dtNode, names ...string) bool {
	for _, c := range stringsDT(n.props["compatible"]) {
		for _, name := range names {
			if c == name {
				return true
			}
		}
	}
	return false
}

// ValidateActiveDTB checks the tree container. Binding interpretation belongs
// to the component mapper, so unrelated vendor nodes cannot block collection.
func ValidateActiveDTB(data []byte) error {
	if len(data) > 8<<20 {
		return errors.New("active DT exceeds size limit")
	}
	_, e := parseFDT(data)
	return e
}
func cellBytes(values ...uint32) []byte {
	b := make([]byte, 4*len(values))
	for i, v := range values {
		be.PutUint32(b[i*4:], v)
	}
	return b
}

// AdaptActiveDTB maps the MSM8916 USB/eMMC/WCNSS/modem platform and its GPIO
// controls onto the Linux #17 bindings. It does not infer a marketing model.
func AdaptActiveDTB(active, base []byte) ([]byte, error) {
	source, e := parseFDT(active)
	if e != nil {
		return nil, e
	}
	target, e := parseFDT(base)
	if e != nil {
		return nil, e
	}
	if !compatibleNode(source[0], "qcom,msm8916", "qcom,apq8016") {
		return nil, errors.New("active DT lacks the MSM8916/APQ8016 platform binding")
	}
	if !compatibleNode(target[0], "qcom,msm8916", "qcom,apq8016") {
		return nil, errors.New("target DT lacks the MSM8916/APQ8016 platform binding")
	}
	for _, n := range source {
		if !n.enabled {
			continue
		}
		for _, c := range stringsDT(n.props["compatible"]) {
			if !strings.HasPrefix(c, "qcom,wcn3") {
				continue
			}
			mapped := false
			for _, dest := range target {
				if compatibleNode(dest, c) {
					mapped = true
					break
				}
			}
			if !mapped {
				return nil, fmt.Errorf("radio component %s has no target binding", c)
			}
		}
	}
	groups := [][]string{
		{"qcom,hsusb-otg", "qcom,ci-hdrc"},
		{"qcom,sdhci-msm", "qcom,msm8916-sdhci"},
		{"qcom,wcnss_wlan", "qcom,wcn3620", "qcom,wcnss-wlan"},
		{"qcom,pil-q6v5-mss", "qcom,pil-q6v56-mss", "qcom,msm8916-mss-pil"},
		{"qcom,spmi-pmic-arb"},
	}
	for _, names := range groups {
		found := false
		for _, n := range source {
			if n.enabled && compatibleNode(n, names...) {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("active DT lacks a supported component: %s", strings.Join(names, " / "))
		}
	}
	// These addresses identify the supported SoC components, independently of
	// DT node names, phandle allocation or the board's marketing identity.
	for _, component := range []struct {
		names   []string
		address uint32
	}{
		{[]string{"qcom,hsusb-otg", "qcom,ci-hdrc"}, 0x078d9000},
		{[]string{"qcom,sdhci-msm", "qcom,msm8916-sdhci"}, 0x07824000},
		{[]string{"qcom,spmi-pmic-arb"}, 0x0200f000},
	} {
		found := false
		for _, n := range source {
			if !n.enabled || !compatibleNode(n, component.names...) {
				continue
			}
			reg, _ := cells(n.props["reg"])
			for _, address := range reg {
				if address == component.address {
					found = true
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("active DT lacks component register mapping at 0x%x", component.address)
		}
	}
	pmic := false
	for _, n := range source {
		if n.enabled && (compatibleNode(n, "qcom,pm8916") || strings.HasPrefix(filepath.Base(n.path), "qcom,pm8916@")) {
			pmic = true
		}
	}
	if !pmic {
		return nil, errors.New("active DT does not identify the PM8916 power controller")
	}
	findHandle := func(nodes []*dtNode, value []byte) *dtNode {
		if len(value) != 4 {
			return nil
		}
		id := be.Uint32(value)
		for _, n := range nodes {
			for _, key := range []string{"phandle", "linux,phandle"} {
				if len(n.props[key]) == 4 && be.Uint32(n.props[key]) == id {
					return n
				}
			}
		}
		return nil
	}
	// Carry the running kernel's RAM banks across the root cell-width change.
	width := func(root *dtNode, key string, def int) int {
		if len(root.props[key]) == 4 {
			return int(be.Uint32(root.props[key]))
		}
		return def
	}
	sa, ss := width(source[0], "#address-cells", 2), width(source[0], "#size-cells", 1)
	ta, ts := width(target[0], "#address-cells", 2), width(target[0], "#size-cells", 1)
	var memory []byte
	for _, n := range source {
		if !n.enabled || string(n.props["device_type"]) != "memory\x00" {
			continue
		}
		reg, e := cells(n.props["reg"])
		if e != nil || sa < 1 || sa > 2 || ss < 1 || ss > 2 || ta < 1 || ta > 2 || ts < 1 || ts > 2 || len(reg)%(sa+ss) != 0 {
			return nil, errors.New("unsupported active RAM bank encoding")
		}
		for i := 0; i < len(reg); i += sa + ss {
			address, size := uint64(0), uint64(0)
			for _, v := range reg[i : i+sa] {
				address = address<<32 | uint64(v)
			}
			for _, v := range reg[i+sa : i+sa+ss] {
				size = size<<32 | uint64(v)
			}
			if address != 0x80000000 || size < 256<<20 || address+size > 0x100000000 {
				return nil, errors.New("active RAM banks do not fit the Linux #17 memory map")
			}
			if ta == 2 {
				memory = append(memory, cellBytes(uint32(address>>32))...)
			}
			memory = append(memory, cellBytes(uint32(address))...)
			if ts == 2 {
				memory = append(memory, cellBytes(uint32(size>>32))...)
			}
			memory = append(memory, cellBytes(uint32(size))...)
		}
	}
	if len(memory) == 0 {
		return nil, errors.New("active DT lacks RAM bank sizes")
	}
	for _, n := range target {
		if string(n.props["device_type"]) == "memory\x00" {
			n.props["reg"] = memory
		}
	}
	for _, n := range target {
		if !compatibleNode(n, "qcom,msm8916-sdhci") {
			continue
		}
		reg, _ := cells(n.props["reg"])
		isEMMC := false
		for _, v := range reg {
			if v == 0x07824000 {
				isEMMC = true
			}
		}
		if !isEMMC {
			continue
		}
		for _, s := range source {
			if !s.enabled || !compatibleNode(s, "qcom,sdhci-msm", "qcom,msm8916-sdhci") {
				continue
			}
			reg, _ := cells(s.props["reg"])
			same := false
			for _, v := range reg {
				if v == 0x07824000 {
					same = true
				}
			}
			if !same {
				continue
			}
			width := s.props["bus-width"]
			if len(width) == 0 {
				width = s.props["qcom,bus-width"]
			}
			if len(width) != 4 || be.Uint32(width) != 4 && be.Uint32(width) != 8 {
				return nil, errors.New("active DT lacks eMMC bus width")
			}
			n.props["bus-width"] = append([]byte(nil), width...)
			for _, supply := range []struct{ target, legacy, voltage string }{{"vmmc-supply", "vdd-supply", "qcom,vdd-voltage-level"}, {"vqmmc-supply", "vdd-io-supply", "qcom,vdd-io-voltage-level"}} {
				ref := s.props[supply.target]
				if len(ref) == 0 {
					ref = s.props[supply.legacy]
				}
				rail := findHandle(source, ref)
				if rail == nil {
					return nil, fmt.Errorf("active eMMC lacks %s", supply.legacy)
				}
				name := strings.TrimSuffix(string(rail.props["regulator-name"]), "\x00")
				if name == "" {
					name = filepath.Base(rail.path)
				}
				if i := strings.LastIndexAny(name, "_-"); i >= 0 {
					name = name[i+1:]
				}
				var dest *dtNode
				for _, candidate := range target {
					if filepath.Base(candidate.path) == name && candidate.parent != nil && compatibleNode(candidate.parent, "qcom,rpm-pm8916-regulators") {
						dest = candidate
						break
					}
				}
				if dest == nil {
					return nil, fmt.Errorf("eMMC regulator %s has no target binding", name)
				}
				min, max := rail.props["regulator-min-microvolt"], rail.props["regulator-max-microvolt"]
				if levels := s.props[supply.voltage]; len(levels) == 8 {
					min, max = levels[:4], levels[4:]
				}
				if len(min) != 4 || len(max) != 4 || be.Uint32(min) < 500000 || be.Uint32(max) > 3600000 || be.Uint32(min) > be.Uint32(max) {
					return nil, fmt.Errorf("eMMC regulator %s lacks voltage bounds", name)
				}
				if len(dest.props["phandle"]) != 4 {
					return nil, fmt.Errorf("target eMMC regulator %s lacks a reference", name)
				}
				dest.props["regulator-min-microvolt"] = append([]byte(nil), min...)
				dest.props["regulator-max-microvolt"] = append([]byte(nil), max...)
				n.props[supply.target] = append([]byte(nil), dest.props["phandle"]...)
			}
		}
	}
	// Kernel memory carveouts, clocks and subsystem protocols belong to the target
	// ABI. Board GPIO devices are reconstructed with the target controller handle.
	var gpio *dtNode
	handles := map[uint32]*dtNode{}
	for _, n := range source {
		for _, key := range []string{"phandle", "linux,phandle"} {
			if len(n.props[key]) == 4 {
				handles[be.Uint32(n.props[key])] = n
			}
		}
	}
	for _, n := range target {
		if compatibleNode(n, "qcom,msm8916-pinctrl") {
			gpio = n
			break
		}
	}
	if gpio == nil || len(gpio.props["phandle"]) != 4 {
		return nil, errors.New("target TLMM controller is missing")
	}
	controller := be.Uint32(gpio.props["phandle"])
	var leds, keys *dtNode
	for _, n := range target {
		if compatibleNode(n, "gpio-leds") {
			leds = n
		}
		if compatibleNode(n, "gpio-keys") {
			keys = n
		}
	}
	if leds == nil || keys == nil {
		return nil, errors.New("target GPIO device groups are missing")
	}
	specs := func(n *dtNode) ([]uint32, error) {
		cs, e := cells(n.props["gpios"])
		if e != nil || len(cs) != 3 {
			return nil, fmt.Errorf("unsupported GPIO specifier at %s", n.path)
		}
		h := handles[cs[0]]
		if h == nil || !compatibleNode(h, "qcom,msm-gpio", "qcom,msm8916-pinctrl") {
			return nil, fmt.Errorf("unsupported GPIO controller at %s", n.path)
		}
		if cs[1] > 121 || cs[2] > 1 {
			return nil, fmt.Errorf("unsupported GPIO number/flags at %s", n.path)
		}
		return []uint32{controller, cs[1], cs[2]}, nil
	}
	// Keep parent pinctrl references, replacing their pin lists with the actual
	// source controls. Disabled source nodes do not become active outputs.
	rebuilt := []*dtNode{}
	for _, parent := range []*dtNode{leds, keys} {
		var controls []*dtNode
		for _, n := range source {
			if n.parent == nil || !n.enabled || !compatibleNode(n.parent, stringsDT(parent.props["compatible"])...) {
				continue
			}
			if _, ok := n.props["gpios"]; ok {
				controls = append(controls, n)
			}
		}
		var pins []string
		used := map[uint32]bool{}
		for i, n := range controls {
			cs, e := specs(n)
			if e != nil {
				return nil, e
			}
			if parent == leds && cs[1] <= 3 && strings.Contains(strings.ToLower(string(n.props["label"])), "sim") {
				matched := false
				for _, existing := range target {
					v, _ := cells(existing.props["gpios"])
					if existing.parent == parent && len(v) == 3 && v[1] == cs[1] && v[2] == cs[2] && string(existing.props["default-state"]) == string(n.props["default-state"]) {
						matched = true
						rebuilt = append(rebuilt, existing)
						pins = append(pins, fmtGPIO(cs[1]))
						used[cs[1]] = true
					}
				}
				if !matched {
					return nil, fmt.Errorf("SIM control GPIO%d differs from target binding", cs[1])
				}
				continue
			}
			if parent == leds && strings.Contains(strings.ToLower(string(n.props["label"])), "sim") {
				return nil, errors.New("SIM control requires a different target binding")
			}
			if used[cs[1]] {
				return nil, fmt.Errorf("duplicate GPIO%d in %s", cs[1], parent.path)
			}
			used[cs[1]] = true
			pins = append(pins, fmtGPIO(cs[1]))
			out := &dtNode{path: fmt.Sprintf("%s/control-%d", parent.path, i), parent: parent, enabled: true, props: map[string][]byte{"gpios": cellBytes(cs...)}}
			for _, key := range []string{"label", "default-state", "linux,default-trigger", "linux,code", "debounce-interval", "wakeup-source"} {
				if v, ok := n.props[key]; ok {
					out.props[key] = append([]byte(nil), v...)
				}
			}
			if parent == keys && (strings.Contains(strings.ToLower(filepath.Base(n.path)+" "+string(n.props["label"])), "reset") || strings.Contains(strings.ToLower(string(n.props["label"])), "restart")) {
				out.props["linux,code"] = cellBytes(0x198)
			}
			if parent == leds {
				label := strings.ToLower(string(n.props["label"]))
				role := "indicator"
				if strings.Contains(label, "wifi") || strings.Contains(label, "wlan") {
					role = "wifi"
				} else if strings.Contains(label, "internet") || strings.Contains(label, "4g") || strings.Contains(label, "lte") {
					role = "internet"
				} else if strings.Contains(label, "power") || strings.Contains(label, ":os") {
					role = "os"
				}
				out.props["label"] = []byte(fmt.Sprintf("led%d:%s\x00", i, role))
			}
			rebuilt = append(rebuilt, out)
		}
		refs, e := cells(parent.props["pinctrl-0"])
		if e != nil {
			return nil, e
		}
		for _, ref := range refs {
			for _, n := range target {
				if len(n.props["phandle"]) == 4 && be.Uint32(n.props["phandle"]) == ref {
					sort.Strings(pins)
					n.props["pins"] = []byte(strings.Join(pins, "\x00") + "\x00")
				}
			}
		}
		if len(pins) == 0 {
			parent.props["status"] = []byte("disabled\x00")
		} else {
			parent.props["status"] = []byte("okay\x00")
		}
	}
	kept := target[:0]
	for _, n := range target {
		if n.parent == leds || n.parent == keys {
			continue
		}
		kept = append(kept, n)
	}
	kept = append(kept, rebuilt...)
	// Target-only GPIO outputs must not survive a board-specific source mapping.
	out, e := encodeFDT(base, kept)
	if e != nil {
		return nil, e
	}
	v, e := ParseDTB(out, "automatic-target", 0)
	if e != nil {
		return nil, e
	}
	if len(v.Issues) > 0 {
		return nil, fmt.Errorf("target resource conflict: %s", strings.Join(v.Issues, "; "))
	}
	return out, nil
}

func AutomaticProfile(r HardwareReport, inputs AutomaticInputs) (HardwareProfile, error) {
	inputs.ActiveDTB = append([]byte(nil), inputs.ActiveDTB...)
	inputs.BaseDTB = append([]byte(nil), inputs.BaseDTB...)
	bundleInputs := map[string]string{}
	for k, v := range inputs.Bundle {
		bundleInputs[k] = v
	}
	inputs.Bundle = bundleInputs
	if ValidateInfo(r.Device) != nil {
		return HardwareProfile{}, errors.New("device geometry is unavailable")
	}
	for _, area := range []string{"user", "boot1", "boot2"} {
		if r.Device.Geometry.Areas[area] == 0 {
			return HardwareProfile{}, &UnsupportedInput{Code: "storage-geometry", Message: "Нет геометрии области: " + area}
		}
	}
	dtb, e := AdaptActiveDTB(inputs.ActiveDTB, inputs.BaseDTB)
	if e != nil {
		return HardwareProfile{}, &UnsupportedInput{Code: "active-dtb-binding", Message: e.Error()}
	}
	recipe, layout := "preserve-gpt-v1", "boot-rootfs"
	for _, part := range r.GPT.Partitions {
		if part.Name == "system" {
			recipe, layout = StockRecipe, "stock"
		}
	}
	if !supportedLayout(r, layout) {
		return HardwareProfile{}, &UnsupportedInput{Code: "storage-layout", Message: "Нет boot и system/userdata или rootfs для установки"}
	}
	for _, name := range []string{"modem", "persist", "tz", "hyp"} {
		found := false
		for _, part := range r.GPT.Partitions {
			if part.Name == name {
				found = true
			}
		}
		if !found {
			return HardwareProfile{}, &UnsupportedInput{Code: "storage-input", Message: "Отсутствует необходимый раздел: " + name}
		}
	}
	bundle := map[string]string{}
	for _, name := range []string{"kernel", "modules", "mba"} {
		if requireHash(inputs.Bundle[name]) != nil {
			return HardwareProfile{}, fmt.Errorf("automatic profile lacks %s input", name)
		}
		bundle[name] = inputs.Bundle[name]
	}
	bundle["dtb"] = HashBytes(dtb)
	p := HardwareProfile{Version: Schema, ID: "msm8916-" + HashBytes(dtb)[:16], KernelBuild: 17, Bundle: bundle, FirmwareSource: "own-device", Recipe: recipe, BootFirmwarePolicy: BootFirmwarePolicy, Layout: layout, RequiredAreas: []string{"user", "boot1", "boot2"}}
	p.automatic = &automaticProfile{inputs: inputs, digest: Hash(p), device: Hash(r.Device), storage: StorageFingerprint(r), sources: SourceSetFingerprint(r.Variants), target: dtb}
	return p, nil
}
func (p HardwareProfile) AutomaticInputs() *AutomaticInputs {
	if p.automatic == nil {
		return nil
	}
	b := p.automatic.inputs
	b.ActiveDTB = append([]byte(nil), b.ActiveDTB...)
	b.BaseDTB = append([]byte(nil), b.BaseDTB...)
	b.Bundle = map[string]string{}
	for k, v := range p.automatic.inputs.Bundle {
		b.Bundle[k] = v
	}
	return &b
}
func (p HardwareProfile) automaticMatches(r HardwareReport) bool {
	a := p.automatic
	return a != nil && a.digest == Hash(p) && a.device == Hash(r.Device) && a.storage == StorageFingerprint(r) && a.sources == SourceSetFingerprint(r.Variants)
}

// DTBFromProperties reconstructs a flattened tree from the live OF filesystem.
// Paths are rooted at the supplied tree; no host/device identity is embedded.
func DTBFromProperties(properties map[string]map[string][]byte) ([]byte, error) {
	if len(properties) == 0 || len(properties) > 8192 {
		return nil, errors.New("live device tree is empty or too large")
	}
	var paths []string
	for path := range properties {
		if path != "/" && (!strings.HasPrefix(path, "/") || strings.Contains(path, "..")) {
			return nil, errors.New("invalid live DT path")
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	byPath := map[string]*dtNode{}
	nodes := []*dtNode{}
	for _, path := range paths {
		n := &dtNode{path: path, props: properties[path], enabled: true}
		if path != "/" {
			n.parent = byPath[filepath.Dir(path)]
			if n.parent == nil {
				return nil, errors.New("live DT parent missing")
			}
		}
		byPath[path] = n
		nodes = append(nodes, n)
	}
	if byPath["/"] == nil {
		return nil, errors.New("live DT root missing")
	}
	raw := make([]byte, 56)
	be.PutUint32(raw, fdtMagic)
	be.PutUint32(raw[16:], 40)
	be.PutUint32(raw[20:], 17)
	be.PutUint32(raw[24:], 16)
	return encodeFDT(raw, nodes)
}

// BindActiveDTB checks platform/board selectors already observed on the attached
// storage. Runtime fixups and overlays do not require byte-identical source DTBs.
func BindActiveDTB(r HardwareReport, active []byte) error {
	live, e := parseFDT(active)
	if e != nil {
		return e
	}
	if len(r.Variants) == 0 {
		return nil
	}
	for _, source := range r.Variants {
		for _, a := range stringsDT(live[0].props["compatible"]) {
			for _, b := range source.Compatible {
				if a == b && (a == "qcom,msm8916" || a == "qcom,apq8016") {
					return nil
				}
			}
		}
	}
	return &UnsupportedInput{Code: "active-dtb-source", Message: "Активное дерево и исходный набор относятся к разным платформам"}
}

// SourceDTB selects a unique/equivalent source tree when no live tree is needed
// to disambiguate a container. Different hardware maps require live observation.
func SourceDTB(ctx context.Context, d Reader, r HardwareReport) ([]byte, error) {
	if len(r.Variants) == 0 || len(r.Issues) > 0 {
		return nil, nil
	}
	chosen := r.Variants[0]
	if r.AppliedSHA256 != "" {
		found := false
		for _, v := range r.Variants {
			if v.SHA256 == r.AppliedSHA256 {
				chosen = v
				found = true
			}
		}
		if !found {
			return nil, nil
		}
	} else {
		for _, v := range r.Variants[1:] {
			if chosen.CompatibilitySHA256 == "" || v.CompatibilitySHA256 != chosen.CompatibilitySHA256 {
				return nil, nil
			}
		}
	}
	for _, part := range r.GPT.Partitions {
		if part.Name == chosen.Source && chosen.Offset <= part.Range.Length && chosen.Bytes <= part.Range.Length-chosen.Offset {
			sector := r.Device.Geometry.SectorSize
			start := chosen.Offset / sector * sector
			end := (chosen.Offset + chosen.Bytes + sector - 1) / sector * sector
			if end > part.Range.Length {
				return nil, errors.New("source DTB extends outside its partition")
			}
			b, e := ReadRange(ctx, d, Range{part.Range.Area, part.Range.Offset + start, end - start}, 16<<20+2*sector)
			if e != nil {
				return nil, e
			}
			return b[chosen.Offset-start : chosen.Offset-start+chosen.Bytes], nil
		}
	}
	return nil, nil
}
