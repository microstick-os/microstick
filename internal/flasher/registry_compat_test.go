package flasher

func resolveCatalogV1(report HardwareReport, profiles []HardwareProfile) Resolution {
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
