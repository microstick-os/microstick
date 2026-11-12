package flasher

import (
	"bytes"
	"context"
	"errors"
	"sort"
)

const StockRecipe = "hmuf02-stock-gpt-v1"
const LK2ndOffset = 512 * 1024
const LK2ndBytes = 428048
const LK2ndSHA256 = "8f5f788b45b96447517ff121f20841e6bfc95a5d7c4b48fdedb855d90f593144"
const LK2ndURL = "https://github.com/msm8916-mainline/lk2nd/releases/download/23.1/lk2nd-msm8916.img"

type BootProvenance struct {
	Mode        string `json:"mode"`
	OuterSHA256 string `json:"outer_sha256,omitempty"`
	InnerOffset uint64 `json:"inner_offset"`
	DTBOrigin   string `json:"dtb_origin"`
}

// BootDTBs never mistakes lk2nd's own QCDT for the installed OS's DTB.
func BootDTBs(b []byte) (BootProvenance, []DTVariant, error) {
	p := BootProvenance{Mode: "direct", DTBOrigin: "boot"}
	if len(b) >= LK2ndBytes && HashBytes(b[:LK2ndBytes]) == LK2ndSHA256 {
		p = BootProvenance{Mode: "lk2nd-23.1", OuterSHA256: LK2ndSHA256, InnerOffset: LK2ndOffset, DTBOrigin: "boot:inner"}
		if len(b) < LK2ndOffset+8 || string(b[LK2ndOffset:LK2ndOffset+8]) != "ANDROID!" || !allZero(b[LK2ndBytes:LK2ndOffset]) {
			return p, nil, errors.New("lk2nd: отсутствует внутренний boot на512KiB или повреждён padding")
		}
		vs, e := DTBs(b[LK2ndOffset:], "boot")
		for i := range vs {
			vs[i].Offset += LK2ndOffset
		}
		return p, vs, e
	}
	// An inner boot signature with an unknown outer image is never accepted.
	if len(b) > LK2ndOffset+8 && string(b[LK2ndOffset:LK2ndOffset+8]) == "ANDROID!" {
		return p, nil, errors.New("неизвестный внешний загрузчик составного boot")
	}
	vs, e := DTBs(b, "boot")
	return p, vs, e
}
func allZero(b []byte) bool { return bytes.Count(b, []byte{0}) == len(b) }
func CompositeBoot(lk, inner []byte, capacity uint64) ([]byte, error) {
	if len(lk) != LK2ndBytes || HashBytes(lk) != LK2ndSHA256 || len(inner) < 8 || string(inner[:8]) != "ANDROID!" {
		return nil, errors.New("требуется pinned lk2nd23.1 и Android boot")
	}
	if uint64(LK2ndOffset+len(inner)) > capacity {
		return nil, errors.New("составной boot не помещается в boot")
	}
	b := make([]byte, LK2ndOffset+len(inner))
	copy(b, lk)
	copy(b[LK2ndOffset:], inner)
	return b, nil
}

// This fingerprint includes selectors, board/revision, map and exact findings.
// It intentionally excludes offsets (container dependent) and device identity.
func SourceSetFingerprint(vs []DTVariant) string {
	type source struct {
		SHA256, Map string
		Board       []uint32
		Selectors   [][]uint32
		Issues      []string
	}
	set := make([]source, 0, len(vs))
	for _, v := range vs {
		set = append(set, source{v.SHA256, v.MapSHA256, v.BoardID, v.Selectors, v.Issues})
	}
	sort.Slice(set, func(i, j int) bool { return set[i].SHA256 < set[j].SHA256 })
	return Hash(set)
}
func reviewedSet(r HardwareReport, p HardwareProfile) string {
	if p.Board == "" || len(r.Issues) > 0 {
		return ""
	}
	layout := ""
	for _, part := range r.GPT.Partitions {
		if part.Name == "system" {
			layout = "stock"
		}
		if part.Name == "rootfs" {
			layout = "boot-rootfs"
		}
	}
	if p.Layout != "" && p.Layout != layout {
		return ""
	}
	if !supportedLayout(r, layout) {
		return ""
	}
	h := SourceSetFingerprint(r.Variants)
	compatible := CompatibilitySetFingerprint(r.Variants)
	for _, s := range p.SourceSets {
		if (s.Fingerprint == h || compatible != "" && s.CompatibilityFingerprint == compatible) && s.Review != "" {
			return s.ID
		}
	}
	return ""
}

func CompatibilitySetFingerprint(vs []DTVariant) string {
	var keys []string
	seen := map[string]bool{}
	for _, v := range vs {
		if requireHash(v.CompatibilitySHA256) != nil {
			return ""
		}
		key := Hash([]any{v.CompatibilitySHA256, v.BoardID, v.Selectors, v.Issues})
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	return Hash(keys)
}

// The recipe uses inspected partition addresses. A different eMMC capacity or
// per-unit GUID is not a different board when the reviewed DT set is identical.
func supportedLayout(r HardwareReport, layout string) bool {
	names := map[string]bool{}
	for _, p := range r.GPT.Partitions {
		names[p.Name] = true
		if !p.Range.Valid(r.Device.Geometry) {
			return false
		}
	}
	if !names["boot"] {
		return false
	}
	if layout == "stock" {
		return names["system"] && names["userdata"] && !names["rootfs"]
	}
	if layout == "boot-rootfs" {
		return names["rootfs"] && !names["system"]
	}
	return false
}

// StorageRecipe is persisted in the receipt, attestation and exact write plan.
// Partition GUIDs come from the inspected device.
type StorageRecipe struct {
	Recipe         string    `json:"recipe"`
	Source         string    `json:"source"`
	BootMode       string    `json:"boot_mode"`
	Boot           Partition `json:"boot"`
	Root           Partition `json:"root"`
	Data           Partition `json:"data"`
	DataMode       string    `json:"data_mode"` // format or preserve
	EraseConfirmed bool      `json:"erase_confirmed"`
}

func StockStorage(r HardwareReport, clean, eraseConfirmed bool) (StorageRecipe, error) {
	s := StorageRecipe{Recipe: StockRecipe, BootMode: "lk2nd-23.1+512KiB", Source: "stock", DataMode: "format", EraseConfirmed: eraseConfirmed}
	for _, p := range r.GPT.Partitions {
		switch p.Name {
		case "boot":
			s.Boot = p
		case "system":
			s.Root = p
		case "userdata":
			s.Data = p
		case "rootfs":
			return s, errors.New("boot/rootfs layout требует preserve-gpt recipe")
		}
	}
	if r.Boot.Mode == "lk2nd-23.1" {
		s.Source = "microstick"
		if !clean {
			s.DataMode = "preserve"
		}
	}
	if e := s.Validate(r.Device.Geometry); e != nil {
		return s, e
	}
	return s, nil
}
func (s StorageRecipe) Validate(g Geometry) error {
	if s.Recipe != StockRecipe || s.BootMode != "lk2nd-23.1+512KiB" || s.Boot.Name != "boot" || s.Root.Name != "system" || s.Data.Name != "userdata" || s.Boot.GUID == "" || s.Root.GUID == "" || s.Data.GUID == "" || s.Boot.Range.Area != "user" || s.Root.Range.Area != "user" || s.Data.Range.Area != "user" {
		return errors.New("неверная stock storage mapping")
	}
	if s.Source != "stock" && s.Source != "microstick" || s.DataMode != "format" && s.DataMode != "preserve" || s.Source == "stock" && s.DataMode != "format" || s.DataMode == "format" && !s.EraseConfirmed {
		return errors.New("очистка userdata требует отдельного подтверждения")
	}
	return validateRanges([]Range{s.Boot.Range, s.Root.Range, s.Data.Range}, g)
}
func protectedRanges(r HardwareReport, targets []Range) []Range {
	// Include every physical byte outside targets, including free gaps and both GPTs.
	out := []Range{}
	for area, total := range r.Device.Geometry.Areas {
		pos := uint64(0)
		for _, target := range sortedRanges(targets) {
			if target.Area != area {
				continue
			}
			if pos < target.Offset {
				out = append(out, Range{area, pos, target.Offset - pos})
			}
			pos = target.Offset + target.Length
		}
		if pos < total {
			out = append(out, Range{area, pos, total - pos})
		}
	}
	return sortedRanges(out)
}
func verifyProtected(ctx context.Context, d Reader, guards []Extent) error {
	for _, x := range guards {
		h, e := HashRange(ctx, d, x.Range)
		if e != nil {
			return e
		}
		if h != x.SHA256 {
			return errors.New("изменён защищённый диапазон/GPT; reset не выполнен")
		}
	}
	return nil
}

// StorageFingerprint excludes unit GUIDs but binds every name, address, size and
// physical area. A reviewed stock DT container cannot authorize an unknown GPT.
func StorageFingerprint(r HardwareReport) string {
	type part struct {
		Name  string
		Range Range
	}
	parts := []part{}
	for _, p := range r.GPT.Partitions {
		parts = append(parts, part{p.Name, p.Range})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Name < parts[j].Name })
	return Hash([]any{r.Device.Geometry, parts})
}
