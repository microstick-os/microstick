package flasher

import (
	"context"
	"errors"
	"fmt"
)

var requiredSubsystems = []string{"gpio", "pinctrl", "interrupts", "clocks", "regulators", "memory", "reserved-memory", "led", "buttons", "sim", "wifi", "usb", "power"}

// Board/kernel compatibility is distinct from the attached unit's stock DSP.
// MPSS/WCNSS hashes identify device inputs; they need not match the profile.
func CompatibleBundle(p HardwareProfile, bundle map[string]string) bool {
	if len(bundle) != len(p.Bundle) {
		return false
	}
	for key, expected := range p.Bundle {
		if requireHash(bundle[key]) != nil {
			return false
		}
		if p.FirmwareSource == "own-device" && (key == "mpss" || key == "wcnss") {
			continue
		}
		if bundle[key] != expected {
			return false
		}
	}
	return true
}

func Resolve(report HardwareReport, profiles []HardwareProfile) Resolution {
	result := Resolution{Status: "unsupported", Reasons: []string{}}
	if len(report.Issues) > 0 {
		result.Status = "conflict"
		result.Reasons = append(result.Reasons, report.Issues...)
		return result
	}
	// Only an exact reviewed source set and storage layout can
	// interpret downstream telephone alternatives. Generic reports remain strict.
	reviewed := map[string]string{}
	for _, p := range profiles {
		if id := reviewedSet(report, p); id != "" {
			reviewed[p.ID] = id
		}
	}
	variants := report.Variants
	if len(variants) == 0 {
		result.Reasons = append(result.Reasons, "Нет исходных DTB")
		return result
	}
	if report.AppliedSHA256 != "" {
		variants = nil
		for _, v := range report.Variants {
			if v.SHA256 == report.AppliedSHA256 {
				variants = append(variants, v)
			}
		}
		if len(variants) != 1 {
			result.Status = "conflict"
			result.Reasons = append(result.Reasons, "Применённый DTB не найден в исходном наборе")
			return result
		}
	}
	for _, v := range variants {
		if len(v.Issues) > 0 && len(reviewed) == 0 {
			result.Status = "conflict"
			result.Reasons = append(result.Reasons, v.Issues...)
			return result
		}
	}
	if report.AppliedSHA256 == "" && len(variants) > 1 && len(reviewed) == 0 {
		// Equivalence is an explicit whole-map property. Listing several
		// hashes in a profile does not prove which DTB the bootloader chose.
		for _, v := range variants[1:] {
			if v.MapSHA256 != variants[0].MapSHA256 {
				result.Status = "ambiguous"
				result.Reasons = append(result.Reasons, "Несколько неэквивалентных DTB; применённый вариант не доказан")
				return result
			}
		}
	}
	contains := func(xs []string, s string) bool {
		for _, x := range xs {
			if x == s {
				return true
			}
		}
		return false
	}
	matched := []HardwareProfile{}
	for _, p := range profiles {
		if p.Version != Schema {
			continue
		}
		if p.Board != "" {
			if reviewed[p.ID] == "" {
				continue
			}
			result.Candidates = append(result.Candidates, p.ID)
			matched = append(matched, p)
			continue
		}
		all := true
		for _, v := range variants {
			if !contains(p.DTBHashes, v.SHA256) || !contains(p.MapHashes, v.MapSHA256) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		result.Candidates = append(result.Candidates, p.ID)
		matched = append(matched, p)
	}
	if len(matched) > 1 {
		result.Status = "ambiguous"
		result.Reasons = append(result.Reasons, "Несколько профилей совпали; ручной обход запрещён")
		return result
	}
	if len(matched) == 0 {
		if len(variants) > 1 {
			result.Status = "ambiguous"
		}
		result.Reasons = append(result.Reasons, "Нужны доказательства выбора исходного DTB и полной карты подсистем")
		return result
	}
	p := matched[0]
	result.Profile = p.ID
	if p.Status != "qualified" || !p.Qualified || p.Recipe == "" || p.KernelBuild != 17 {
		result.Reasons = append(result.Reasons, "Профиль candidate: рецепт установки и комплект #17 не квалифицированы")
		return result
	}
	for _, key := range requiredSubsystems {
		if p.Subsystems[key] != "PASS" {
			result.Reasons = append(result.Reasons, "Не подтверждена подсистема: "+key)
		}
	}
	for _, area := range p.RequiredAreas {
		if report.Device.Geometry.Areas[area] == 0 {
			result.Reasons = append(result.Reasons, "Нет физической области: "+area)
		}
	}
	for _, name := range []string{"kernel", "dtb", "modules", "mba", "mpss", "wcnss"} {
		if requireHash(p.Bundle[name]) != nil {
			result.Reasons = append(result.Reasons, "Нет точного hash комплекта: "+name)
		}
	}
	if len(result.Reasons) == 0 {
		result.Status = "matched"
	}
	return result
}
func Inspect(ctx context.Context, d Reader, profiles []HardwareProfile, emitFn Emit) (HardwareReport, error) {
	r := HardwareReport{Version: Schema, Physical: map[string]string{}}
	for _, key := range []string{"boot", "usb", "wifi", "lte", "led"} {
		r.Physical[key] = "NOT_TESTED"
	}
	var e error
	r.Device, e = d.Info(ctx)
	if e != nil {
		return r, e
	}
	if e = ValidateInfo(r.Device); e != nil {
		return r, e
	}
	r.GPT, e = ReadGPT(ctx, d, r.Device)
	if e != nil {
		return r, e
	}
	if native, ok := d.(*EDLReader); ok {
		if native.identity.Kind != "storage" {
			if e = native.BindStorage(ctx, r.GPT.Partitions); e != nil {
				return r, e
			}
		}
		r.Device, e = d.Info(ctx)
		if e != nil {
			return r, e
		}
	}
	emit(emitFn, "inspect", 1, 3, 5, "GPT и физическая геометрия проверены")
	for _, p := range r.GPT.Partitions {
		if p.Name != "boot" && p.Name != "dtb" && p.Name != "dtbo" && p.Name != "vendor_boot" {
			continue
		}
		b, err := ReadRange(ctx, d, p.Range, 128<<20)
		if err != nil {
			return r, err
		}
		var vs []DTVariant
		if p.Name == "boot" {
			r.Boot, vs, err = BootDTBs(b)
			if err == nil && r.Boot.Mode == "lk2nd-23.1" && len(vs) == 1 {
				r.AppliedSHA256 = vs[0].SHA256
			}
		} else {
			vs, err = DTBs(b, p.Name)
		}
		if err != nil {
			r.Issues = append(r.Issues, fmt.Sprintf("%s: %v", p.Name, err))
			continue
		}
		r.Variants = append(r.Variants, vs...)
	}
	if len(r.Variants) == 0 {
		r.Issues = append(r.Issues, "Исходные DTB не найдены")
	}
	r.Resolution = Resolve(r, profiles)
	if r.Resolution.Status == "matched" && nativeIdentity(r.Device.Identity) {
		for _, profile := range profiles {
			if profile.ID == r.Resolution.Profile {
				r.BootFirmware, e = InspectBootFirmware(ctx, d, r, profile.BootFirmwarePolicy)
				if e != nil {
					r.InputFailure = "boot-firmware"
					return r, e
				}
			}
		}
	}
	if e = checkDevice(ctx, d, r.Device); e != nil {
		return r, e
	}
	emit(emitFn, "inspect", 3, 3, 15, "Исследование устройства завершено")
	return r, nil
}
func requireMatched(r HardwareReport) error {
	if r.Version != Schema || r.Resolution.Status != "matched" || r.Resolution.Profile == "" {
		return ErrUnsupported
	}
	if len(r.Variants) == 0 {
		return errors.New("нет рассмотренного DTB")
	}
	return nil
}

// SimulatorProfile describes the file-device boot/rootfs simulation.
func SimulatorProfile(r HardwareReport) HardwareProfile {
	p := HardwareProfile{Version: Schema, ID: "file-simulator", Status: "qualified", Qualified: true, Recipe: "simulator-v1", KernelBuild: 17, Subsystems: map[string]string{}, Bundle: map[string]string{}, RequiredAreas: []string{"user", "boot1", "boot2"}}
	for _, k := range requiredSubsystems {
		p.Subsystems[k] = "PASS"
	}
	for _, k := range []string{"kernel", "dtb", "modules", "mba", "mpss", "wcnss"} {
		p.Bundle[k] = HashBytes([]byte("simulation only: " + k))
	}
	for _, v := range r.Variants {
		p.DTBHashes = append(p.DTBHashes, v.SHA256)
		p.MapHashes = append(p.MapHashes, v.MapSHA256)
	}
	return p
}
