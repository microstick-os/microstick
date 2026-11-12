package flasher

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
)

// Boot firmware policy for the MSM8916/APQ8016 ARM64 ABI.
const BootFirmwarePolicy = "msm8916-arm64-v1"
const BootTZFile = "boot-firmware-tz.mbn"
const BootHYPFile = "boot-firmware-hyp.mbn"
const BootTZSHA256 = "20994995212250902d47611ffd4a201db8e8ed70bbd7daf580656eafe2935b21"
const BootHYPSHA256 = "0680b42298a7d691161e427543211f3ec528b21c901de6e5b13e833e772776cf"
const BootTZBytes = 471040
const BootHYPBytes = 12288

type BootFirmware struct {
	Version   int       `json:"version"`
	Policy    string    `json:"policy"`
	TZ        Partition `json:"tz"`
	HYP       Partition `json:"hyp"`
	TZClass   int       `json:"tz_elf_class"`
	HYPClass  int       `json:"hyp_elf_class"`
	TZSHA256  string    `json:"tz_partition_sha256"`
	HYPSHA256 string    `json:"hyp_partition_sha256"`
	Upgrade   bool      `json:"upgrade"`
	Security  string    `json:"secure_boot"`
}

// JSON observations never substitute for the live read-only hardware probe.
type SecureBootReader interface {
	SecureBoot(context.Context) (string, error)
}

func firmwareELFClass(b []byte, capacity uint64) (int, error) {
	if len(b) < 64 || string(b[:4]) != "\x7fELF" || b[5] != 1 || b[6] != 1 || le.Uint16(b[16:]) != 2 || le.Uint32(b[20:]) != 1 {
		return 0, errors.New("invalid boot firmware ELF")
	}
	var off uint64
	var count, size uint16
	switch b[4] {
	case 1:
		if le.Uint16(b[18:]) != 40 || le.Uint16(b[40:]) != 52 {
			return 0, errors.New("ELF32 ARM header")
		}
		off = uint64(le.Uint32(b[28:]))
		size = le.Uint16(b[42:])
		count = le.Uint16(b[44:])
		if size != 32 {
			return 0, errors.New("ELF32 program headers")
		}
	case 2: // Qualcomm's ELF64 firmware containers may retain EM_ARM.
		if le.Uint16(b[18:]) != 40 && le.Uint16(b[18:]) != 183 || le.Uint16(b[52:]) != 64 {
			return 0, errors.New("ELF64 ARM header")
		}
		off = le.Uint64(b[32:])
		size = le.Uint16(b[54:])
		count = le.Uint16(b[56:])
		if size != 56 {
			return 0, errors.New("ELF64 program headers")
		}
	default:
		return 0, errors.New("boot firmware architecture")
	}
	if count == 0 || count > 128 || off > uint64(len(b)) || uint64(count)*uint64(size) > uint64(len(b))-off {
		return 0, errors.New("boot firmware headers truncated")
	}
	loads := 0
	for i := uint64(0); i < uint64(count); i++ {
		p := b[off+i*uint64(size):]
		var start, n, mem, addr, align uint64
		if b[4] == 1 {
			start = uint64(le.Uint32(p[4:]))
			addr = uint64(le.Uint32(p[8:]))
			n = uint64(le.Uint32(p[16:]))
			mem = uint64(le.Uint32(p[20:]))
			align = uint64(le.Uint32(p[28:]))
		} else {
			start = le.Uint64(p[8:])
			addr = le.Uint64(p[16:])
			n = le.Uint64(p[32:])
			mem = le.Uint64(p[40:])
			align = le.Uint64(p[48:])
		}
		if start > capacity || n > capacity-start || start > uint64(len(b)) || n > uint64(len(b))-start {
			return 0, errors.New("boot firmware segment outside partition")
		}
		if le.Uint32(p) == 1 {
			loads++
			if n > mem || addr+mem < addr || align > 0 && align&(align-1) != 0 {
				return 0, errors.New("invalid load segment")
			}
		}
	}
	if loads == 0 {
		return 0, errors.New("no boot firmware load segments")
	}
	return int(b[4]) * 32, nil
}

func InspectBootFirmware(ctx context.Context, d Reader, r HardwareReport, policy string) (*BootFirmware, error) {
	if policy == "" && r.Device.Identity.Kind == "simulator" {
		return nil, nil
	}
	if policy != BootFirmwarePolicy {
		return nil, &UnsupportedInput{Code: "boot-firmware-policy", Message: "Нет проверенного контракта загрузочной firmware для ARM64"}
	}
	f := &BootFirmware{Version: 1, Policy: policy, Security: "unchanged"}
	for _, p := range r.GPT.Partitions {
		switch p.Name {
		case "tz":
			if f.TZ.Name != "" {
				return nil, errors.New("duplicate TZ")
			}
			f.TZ = p
		case "hyp":
			if f.HYP.Name != "" {
				return nil, errors.New("duplicate HYP")
			}
			f.HYP = p
		}
	}
	classes := []*int{&f.TZClass, &f.HYPClass}
	hashes := []*string{&f.TZSHA256, &f.HYPSHA256}
	for i, p := range []Partition{f.TZ, f.HYP} {
		if p.Name == "" || p.Range.Area != "user" || !p.Range.Valid(r.Device.Geometry) || p.Range.Length > 8<<20 {
			return nil, &UnsupportedInput{Code: "boot-firmware-mapping", Message: "Нет проверенных разделов TZ/HYP для ARM64"}
		}
		b, e := ReadRange(ctx, d, p.Range, 8<<20)
		if e != nil {
			return nil, e
		}
		*classes[i], e = firmwareELFClass(b, p.Range.Length)
		if e != nil {
			return nil, &UnsupportedInput{Code: "boot-firmware-elf", Message: "Повреждена загрузочная firmware; запись не начата"}
		}
		if e = firmwareMemoryMap(b, p.Name, *classes[i]); e != nil {
			return nil, &UnsupportedInput{Code: "boot-firmware-abi", Message: "Неизвестная карта загрузочной firmware; требуется проверка совместимости"}
		}
		*hashes[i] = HashBytes(b)
		// ELF class alone is not a compatibility promise for unknown firmware.
		if i == 0 && !(f.TZClass == 32 && bytes.Contains(b, []byte("TZ.BF.2.2")) || f.TZClass == 64 && bytes.Contains(b, []byte("TZ.BF.3.0-"))) {
			return nil, &UnsupportedInput{Code: "boot-firmware-abi", Message: "Неизвестный ABI TrustZone; требуется проверка совместимости"}
		}
	}
	f.Upgrade = f.TZClass != 64 || f.HYPClass != 64
	if f.Upgrade {
		if f.TZ.Range.Length < BootTZBytes || f.HYP.Range.Length < BootHYPBytes {
			return nil, &UnsupportedInput{Code: "boot-firmware-capacity", Message: "ARM64 firmware не помещается в исходную разметку"}
		}
		security, e := readSecureBoot(ctx, d)
		if e != nil {
			return nil, e
		}
		f.Security = security
	}
	return f, f.Validate(r.Device.Geometry)
}

// Reviewed MSM8916 secure DDR windows, shared by the stock and ARM64 code.
// A different SoC/firmware memory ABI requires a separately reviewed policy.
func firmwareMemoryMap(b []byte, name string, class int) error {
	base, end := uint64(0x86500000), uint64(0x86700000)
	if name == "hyp" {
		base, end = 0x86400000, 0x86500000
	}
	entry, off, size, count := uint64(le.Uint32(b[24:])), uint64(le.Uint32(b[28:])), uint64(le.Uint16(b[42:])), uint64(le.Uint16(b[44:]))
	if class == 64 {
		entry, off, size, count = le.Uint64(b[24:]), le.Uint64(b[32:]), uint64(le.Uint16(b[54:])), uint64(le.Uint16(b[56:]))
	}
	if entry != base || name == "hyp" && class == 64 && le.Uint16(b[18:]) != 183 {
		return errors.New("unsupported firmware entry/machine")
	}
	for i := uint64(0); i < count; i++ {
		p := b[off+i*size:]
		if le.Uint32(p) != 1 {
			continue
		}
		v, physical, n := uint64(le.Uint32(p[8:])), uint64(le.Uint32(p[12:])), uint64(le.Uint32(p[20:]))
		if class == 64 {
			v, physical, n = le.Uint64(p[16:]), le.Uint64(p[24:]), le.Uint64(p[40:])
		}
		if v != physical || v < base || v >= end || n > end-v {
			return errors.New("unsupported secure firmware memory map")
		}
	}
	return nil
}

func readSecureBoot(ctx context.Context, d Reader) (string, error) {
	probe, ok := d.(SecureBootReader)
	if !ok {
		return "", &UnsupportedInput{Code: "boot-firmware-security", Message: "Нет аппаратной проверки Secure Boot; firmware не разрешена к записи"}
	}
	s, e := probe.SecureBoot(ctx)
	if e != nil || s != "disabled" {
		return "", &UnsupportedInput{Code: "boot-firmware-security", Message: "Secure Boot включён или недоступен; firmware не разрешена к записи"}
	}
	return s, nil
}

func (f *BootFirmware) Validate(g Geometry) error {
	if f == nil || f.Version != 1 || f.Policy != BootFirmwarePolicy || f.TZ.Name != "tz" || f.HYP.Name != "hyp" || f.TZ.GUID == "" || f.HYP.GUID == "" || f.TZClass != 32 && f.TZClass != 64 || f.HYPClass != 32 && f.HYPClass != 64 || f.Upgrade != (f.TZClass != 64 || f.HYPClass != 64) || f.Upgrade && f.Security != "disabled" || !f.Upgrade && f.Security != "unchanged" || requireHash(f.TZSHA256) != nil || requireHash(f.HYPSHA256) != nil {
		return errors.New("неполный контракт ARM64 boot firmware")
	}
	if f.TZ.Range.Area != "user" || f.HYP.Range.Area != "user" || f.TZ.Range.Length > 8<<20 || f.HYP.Range.Length > 8<<20 || f.Upgrade && (f.TZ.Range.Length < BootTZBytes || f.HYP.Range.Length < BootHYPBytes) {
		return errors.New("boot firmware geometry")
	}
	return validateRanges([]Range{f.TZ.Range, f.HYP.Range}, g)
}
func (f *BootFirmware) Files() [][2]string {
	if f == nil || !f.Upgrade {
		return nil
	}
	return [][2]string{{"tz", BootTZFile}, {"hyp", BootHYPFile}}
}

func bindBootFirmware(ctx context.Context, d Reader, r HardwareReport, profile HardwareProfile, att ImageAttestation, images string, p *InstallPlan) error {
	f, e := InspectBootFirmware(ctx, d, r, profile.BootFirmwarePolicy)
	if e != nil {
		return e
	}
	if Hash(f) != Hash(att.BootFirmware) {
		return errors.New("boot firmware изменилась после подготовки; требуется повторная подготовка")
	}
	p.BootFirmware = f
	for _, pair := range f.Files() {
		want := BootTZSHA256
		if pair[0] == "hyp" {
			want = BootHYPSHA256
		}
		h, e := HashFile(filepath.Join(images, pair[1]))
		if e != nil {
			return e
		}
		if h != want || h != att.Images[pair[1]] {
			return errors.New("boot firmware attestation/hash mismatch")
		}
	}
	return nil
}

func validateBootFirmwarePlan(p InstallPlan) error {
	if p.Mode == "restore" {
		if p.BootFirmware != nil {
			return errors.New("restore uses own raw firmware, not upgrade")
		}
		return nil
	}
	if !nativeIdentity(p.Device.Identity) && p.BootFirmware == nil {
		return nil
	}
	if e := p.BootFirmware.Validate(p.Device.Geometry); e != nil {
		return e
	}
	f := p.BootFirmware
	count := 0
	if f.Upgrade {
		count = 2
	}
	if len(p.Operations) < 2+count {
		return errors.New("boot firmware/boot commit group missing")
	}
	if p.Operations[len(p.Operations)-1].File != "boot.img" {
		return errors.New("boot must be written last")
	}
	for i, pair := range f.Files() {
		op := p.Operations[len(p.Operations)-3+i]
		part, want, n := f.TZ, BootTZSHA256, uint64(BootTZBytes)
		if pair[0] == "hyp" {
			part, want, n = f.HYP, BootHYPSHA256, BootHYPBytes
		}
		if op.File != pair[1] || op.SHA256 != want || op.Range != (Range{"user", part.Range.Offset, n}) {
			return errors.New("boot firmware target/order/hash mismatch")
		}
	}
	for i, op := range p.Operations {
		for _, part := range []Partition{f.TZ, f.HYP} {
			if overlaps(op.Range, part.Range) && !(f.Upgrade && i >= len(p.Operations)-3 && i < len(p.Operations)-1) {
				return errors.New("unexpected firmware write")
			}
		}
	}
	return nil
}
func overlaps(a, b Range) bool {
	return a.Area == b.Area && a.Offset < b.Offset+b.Length && b.Offset < a.Offset+a.Length
}

func verifyBootFirmwareExecution(ctx context.Context, d Reader, p InstallPlan) error {
	if p.Mode == "restore" || p.BootFirmware == nil {
		return nil
	}
	f := p.BootFirmware
	if p.qualification == nil || p.qualification.BootFirmwarePolicy != f.Policy {
		return ErrUnsupported
	}
	gpt, e := ReadGPT(ctx, d, p.Device)
	if e != nil {
		return e
	}
	for _, want := range append([]Partition{f.TZ, f.HYP}, p.Targets...) {
		found := false
		for _, got := range gpt.Partitions {
			if got == want {
				found = true
			}
		}
		if !found {
			return errors.New("boot/storage mapping changed")
		}
	}
	if f.Upgrade {
		_, e = readSecureBoot(ctx, d)
		return e
	}
	// Existing compatible firmware stays byte-identical, irrespective of its version/hash.
	for i, part := range []Partition{f.TZ, f.HYP} {
		want := []string{f.TZSHA256, f.HYPSHA256}[i]
		h, e := HashRange(ctx, d, part.Range)
		if e != nil {
			return e
		}
		if h != want {
			return errors.New("existing ARM64 firmware changed")
		}
	}
	return nil
}

func verifyBootFirmwareInputs(p InstallPlan, images string) error {
	if p.Mode == "restore" || !nativeIdentity(p.Device.Identity) {
		return nil
	}
	var att ImageAttestation
	if e := readJSON(filepath.Join(images, "HardwareInputs.json"), &att); e != nil {
		return e
	}
	if att.Version != Schema || !EqualDevice(att.Device, p.Device) || att.Profile != p.Profile || Hash(att.BootFirmware) != Hash(p.BootFirmware) || p.qualification == nil || !CompatibleBundle(*p.qualification, att.Bundle) {
		return errors.New("native image attestation changed")
	}
	h, e := HashFile(filepath.Join(images, "Preparation.json"))
	if e != nil {
		return e
	}
	if h != p.InstallationSHA256 || h != att.ReceiptSHA256 {
		return errors.New("preparation receipt changed")
	}
	if p.PayloadVersion == 1 {
		if att.Images["Payload.json"] != p.PayloadSHA256 {
			return errors.New("payload descriptor differs from attestation")
		}
		seen := map[string]bool{}
		for _, op := range p.Operations {
			if !seen[op.File] {
				h, e := HashFile(filepath.Join(images, op.File))
				if e != nil {
					return e
				}
				if h != att.Images[op.File] {
					return errors.New("payload file differs from attestation")
				}
				seen[op.File] = true
			}
		}
	}
	for _, op := range p.Operations {
		if p.PayloadVersion == 0 && att.Images[op.File] != op.SHA256 {
			return errors.New("image hash differs from attestation")
		}
	}
	return nil
}

func validateNativeTargets(p InstallPlan) error {
	if p.PayloadVersion == 1 {
		return validatePayloadTargets(p)
	}
	names, files := []string{"rootfs", "boot"}, []string{"rootfs.ext4", "boot.img"}
	if p.Recipe == StockRecipe {
		names, files = []string{"userdata", "system", "boot"}, []string{"data.ext4", "rootfs.ext4", "boot.img"}
	}
	extra := 0
	if p.BootFirmware.Upgrade {
		extra = 2
	}
	if len(p.Targets) != len(names) || len(p.Operations) != len(names)+extra || requireHash(p.InstallationSHA256) != nil {
		return errors.New("incomplete native install targets/receipt")
	}
	ranges := []Range{p.BootFirmware.TZ.Range, p.BootFirmware.HYP.Range}
	for i, name := range names {
		part := p.Targets[i]
		opIndex := i
		if i == len(names)-1 {
			opIndex = len(p.Operations) - 1
		}
		op := p.Operations[opIndex]
		if part.Name != name || part.GUID == "" || part.Range.Area != "user" || op.File != files[i] || op.Range.Area != part.Range.Area || op.Range.Offset != part.Range.Offset || op.Range.Length > part.Range.Length {
			return errors.New("native install target mismatch")
		}
		ranges = append(ranges, part.Range)
	}
	if e := validateRanges(ranges, p.Device.Geometry); e != nil {
		return e
	}
	if Hash(protection(p.Guards, p.Operations)) != Hash(p.Protected) {
		return errors.New("native protection mismatch")
	}
	return nil
}
