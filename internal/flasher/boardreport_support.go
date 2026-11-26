package flasher

import (
	"bytes"
	"context"
	"regexp"
	"sort"
	"strings"
)

// BoardSupport retains original registry digests separately from sanitized DTBs.
// These observations support review; they never authorize an installation.
type BoardSupport struct {
	RequiredInputs              []string           `json:"required_inputs,omitempty"`
	Version                     int                `json:"schema_version"`
	DTSourcesComplete           bool               `json:"dt_sources_complete"`
	SourceSetFingerprint        string             `json:"source_set_fingerprint,omitempty"`
	CompatibilitySetFingerprint string             `json:"compatibility_set_fingerprint,omitempty"`
	StorageFingerprint          string             `json:"storage_fingerprint"`
	AppliedDTBSHA256            string             `json:"applied_dtb_sha256,omitempty"`
	Missing                     []string           `json:"missing"`
	BootFirmware                BootFirmwareReport `json:"boot_firmware"`
}

type BootFirmwareReport struct {
	TZ               FirmwareReport `json:"tz"`
	HYP              FirmwareReport `json:"hyp"`
	SecureBoot       string         `json:"secure_boot"`
	SecureBootStatus string         `json:"secure_boot_status"`
}

type FirmwareReport struct {
	Range               Range          `json:"range"`
	Status              string         `json:"status"`
	SHA256              string         `json:"sha256,omitempty"`
	ELFClass            int            `json:"elf_class,omitempty"`
	ELFMachine          uint16         `json:"elf_machine,omitempty"`
	Entry               uint64         `json:"entry,omitempty"`
	ABIFamily           string         `json:"abi_family,omitempty"`
	VersionStrings      []string       `json:"version_strings,omitempty"`
	MemoryMapCompatible bool           `json:"memory_map_compatible"`
	Loads               []FirmwareLoad `json:"loads,omitempty"`
}

type FirmwareLoad struct {
	Virtual     uint64 `json:"virtual"`
	Physical    uint64 `json:"physical"`
	FileBytes   uint64 `json:"file_bytes"`
	MemoryBytes uint64 `json:"memory_bytes"`
	Flags       uint32 `json:"flags"`
}

var firmwareVersionPattern = regexp.MustCompile(`QC_IMAGE_VERSION_STRING=[A-Za-z0-9][A-Za-z0-9._+\-]{0,159}|(?:TZ|HYP|BOOT)\.[A-Za-z0-9][A-Za-z0-9._+\-]{0,155}`)

func firmwareVersionByte(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || strings.ContainsRune("._+-", rune(b))
}

// Software image versions have a restricted alphabet and fixed metadata keys.
// OEM strings and arbitrary printable firmware content are not exported.
func firmwareVersionStrings(b []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, loc := range firmwareVersionPattern.FindAllIndex(b, -1) {
		if loc[0] > 0 && firmwareVersionByte(b[loc[0]-1]) || loc[1] < len(b) && firmwareVersionByte(b[loc[1]]) {
			continue // Do not export a fragment of an oversized or prefixed token.
		}
		if loc[0] > 0 && b[loc[0]-1] == '=' {
			continue // Values of arbitrary metadata keys are not software identifiers.
		}
		version := strings.TrimPrefix(string(b[loc[0]:loc[1]]), "QC_IMAGE_VERSION_STRING=")
		if seen[version] || privateProperty.MatchString(version) || strings.Contains(strings.ToLower(version), "private") {
			continue
		}
		seen[version] = true
		out = append(out, version)
		if len(out) == 8 {
			break
		}
	}
	sort.Strings(out)
	return out
}

// Only software image identifiers and validated numeric ELF fields leave memory.
// Firmware segments and private partition GUIDs do not.
func reportFirmware(ctx context.Context, d Reader, r HardwareReport, name string) FirmwareReport {
	f := FirmwareReport{Status: "missing"}
	var part Partition
	for _, p := range r.GPT.Partitions {
		if p.Name == name {
			if part.Name != "" {
				f.Status = "duplicate-partition"
				return f
			}
			part = p
		}
	}
	if part.Name == "" {
		return f
	}
	f.Range = part.Range
	if part.Range.Area != "user" || !part.Range.Valid(r.Device.Geometry) || part.Range.Length > 8<<20 {
		f.Status = "invalid-range"
		return f
	}
	b, e := ReadRange(ctx, d, part.Range, 8<<20)
	if e != nil {
		f.Status = "read-failed"
		return f
	}
	f.SHA256 = HashBytes(b)
	f.VersionStrings = firmwareVersionStrings(b)
	f.ELFClass, e = firmwareELFClass(b, part.Range.Length)
	if e != nil {
		f.Status = "invalid-elf"
		return f
	}
	f.ELFMachine = le.Uint16(b[18:])
	f.Entry = uint64(le.Uint32(b[24:]))
	off, size, count := uint64(le.Uint32(b[28:])), uint64(le.Uint16(b[42:])), uint64(le.Uint16(b[44:]))
	if f.ELFClass == 64 {
		f.Entry = le.Uint64(b[24:])
		off, size, count = le.Uint64(b[32:]), uint64(le.Uint16(b[54:])), uint64(le.Uint16(b[56:]))
	}
	for i := uint64(0); i < count; i++ {
		p := b[off+i*size:]
		if le.Uint32(p) != 1 {
			continue
		}
		load := FirmwareLoad{uint64(le.Uint32(p[8:])), uint64(le.Uint32(p[12:])), uint64(le.Uint32(p[16:])), uint64(le.Uint32(p[20:])), le.Uint32(p[24:])}
		if f.ELFClass == 64 {
			load = FirmwareLoad{le.Uint64(p[16:]), le.Uint64(p[24:]), le.Uint64(p[32:]), le.Uint64(p[40:]), le.Uint32(p[4:])}
		}
		f.Loads = append(f.Loads, load)
	}
	f.ABIFamily = "unknown"
	if name == "tz" {
		if f.ELFClass == 32 && bytes.Contains(b, []byte("TZ.BF.2.2")) {
			f.ABIFamily = "TZ.BF.2.2"
		} else if f.ELFClass == 64 && bytes.Contains(b, []byte("TZ.BF.3.0-")) {
			f.ABIFamily = "TZ.BF.3.0"
		}
	} else {
		f.ABIFamily = BootFirmwarePolicy
	}
	f.Status = "compatible"
	f.MemoryMapCompatible = firmwareMemoryMap(b, name, f.ELFClass) == nil
	if f.ABIFamily == "unknown" || !f.MemoryMapCompatible {
		f.Status = "unsupported-abi"
	}
	return f
}

func reportBootFirmware(ctx context.Context, d Reader, r HardwareReport) BootFirmwareReport {
	f := BootFirmwareReport{TZ: reportFirmware(ctx, d, r, "tz"), HYP: reportFirmware(ctx, d, r, "hyp"), SecureBoot: "unavailable", SecureBootStatus: "skipped-memory-map"}
	// The fixed read-only probe depends on the numeric memory ABI, not on
	// recognizing the software version. It does not authorize an upgrade.
	if f.TZ.MemoryMapCompatible && f.HYP.MemoryMapCompatible {
		f.SecureBootStatus = "unavailable-reader"
		if probe, ok := d.(SecureBootReader); ok {
			s, e := probe.SecureBoot(ctx)
			if e != nil {
				f.SecureBootStatus = "read-failed"
			} else if s == "enabled" || s == "disabled" {
				f.SecureBoot = s
				f.SecureBootStatus = "performed"
			} else {
				f.SecureBootStatus = "invalid-response"
			}
		}
	}
	return f
}
