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
	"regexp"
	"sort"
	"strings"
	"time"
)

type PartitionHash struct {
	Name   string `json:"name"`
	Range  Range  `json:"range"`
	SHA256 string `json:"sha256"`
}
type DTSource struct {
	Source                      string     `json:"source"`
	Offset                      uint64     `json:"offset"`
	OriginalSHA256              string     `json:"original_sha256"`
	OriginalBytes               uint64     `json:"original_bytes"`
	OriginalMapSHA256           string     `json:"original_map_sha256,omitempty"`
	OriginalCompatibilitySHA256 string     `json:"original_compatibility_sha256,omitempty"`
	BoardID                     []uint32   `json:"board_id,omitempty"`
	InSourceSet                 bool       `json:"in_source_set"`
	ExportSHA256                string     `json:"export_sha256"`
	ExportBytes                 int        `json:"export_bytes"`
	RemovedProperties           int        `json:"removed_properties"`
	Selectors                   [][]uint32 `json:"container_selectors,omitempty"`
}

var hardwareProperty = regexp.MustCompile(`^(compatible|model|status|phandle|linux,phandle|device_type|#.*-cells|#address-cells|#size-cells|reg|ranges|dma-ranges|interrupt.*|gpio-controller|gpio-ranges|gpios|gpio|.*-gpios|.*-gpio|clocks|clock-names|clock-frequency|assigned-clocks|assigned-clock-parents|assigned-clock-rates|resets|reset-names|pinctrl-[0-9]+|pinctrl-names|pins|pinmux|function|bias-.*|drive-strength|input-enable|output-high|output-low|.*-supply|regulator-.*|power-domains|memory-region|no-map|reusable|alignment|size|alloc-ranges|label|linux,default-trigger|default-state|linux,code|debounce-interval|wakeup-source|qcom,msm-id|qcom,board-id|qcom,pmic-id|qcom,cont-splash-enabled|qcom,mdss-.*|qcom,pin-func|qcom,num-grp-pins|qcom,pins|qcom,.*-gpio|qcom,.*-gpios|phys|phy-names|dr_mode|maximum-speed|bus-width|non-removable|cap-sd-highspeed|cap-mmc-highspeed|mmc-.*|sd-uhs-.*|vmmc-supply|vqmmc-supply)$`)
var privateProperty = regexp.MustCompile(`(?i)(serial|imei|imsi|password|passwd|credential|secret|psk|mac-address|macaddr|calib|nvram|nvmem|bootargs|kaslr|rng-seed|random-seed|uuid|unique|token|key|board-data)`)
var safeDTName = regexp.MustCompile(`^[A-Za-z0-9,._+@#?\-]+$`)

// Downstream hardware bindings that are not covered by the standard DT names.
// Keep this list explicit: arbitrary vendor properties can contain private data.
var downstreamHardwareProperty = regexp.MustCompile(`^(qcom,gpio-(err-fatal|err-ready|force-stop|map|parent|proxy-unvote|ramdump-disable|stop-ack|tbl-flags|tbl-label|tbl-num)|qcom,exvbus_gpio|qcom,bus-width|qcom,vdd-voltage-level|qcom,vdd-io-voltage-level|qcom,num-pins|qcom,pin-num|qcom,pin-type-(gp|qdsd|sdc)|qcom,regulator-type|qcom,supply-(name|min-voltage|max-voltage|enable-load|disable-load|pre-on-sleep|post-on-sleep|pre-off-sleep|post-off-sleep)|qcom,proxy-clock-names|active-clock-names|proxy-clock-names|clock-rate|linux,memory-limit|power-domain|iommus|nidnt-gpio-polarity)$`)

func sanitizedDTB(raw []byte) ([]byte, string, int, error) {
	nodes, e := parseFDT(raw)
	if e != nil {
		return nil, "", 0, e
	}
	table := bytes.Buffer{}
	offsets := map[string]uint32{}
	structure := bytes.Buffer{}
	removed := 0
	put := func(v uint32) { _ = binaryWrite(&structure, v) }
	pad := func() {
		for structure.Len()%4 != 0 {
			structure.WriteByte(0)
		}
	}
	var writeNode func(*dtNode) error
	writeNode = func(n *dtNode) error {
		name := filepath.Base(n.path)
		if n.parent == nil {
			name = ""
		}
		if name != "" && !safeDTName.MatchString(name) {
			return errors.New("нестандартное имя узла нельзя безопасно экспортировать")
		}
		put(1)
		structure.WriteString(name)
		structure.WriteByte(0)
		pad()
		keys := sortedPropertyKeys(n.props)
		for _, key := range keys {
			// The chosen node carries boot identity/cmdline/initrd state, never wiring.
			if strings.HasPrefix(n.path, "/chosen") || !(hardwareProperty.MatchString(key) || downstreamHardwareProperty.MatchString(key)) || privateProperty.MatchString(key) || !safeDTName.MatchString(key) {
				removed++
				continue
			}
			b := n.props[key]
			if len(b) > 1<<20 {
				return errors.New("DT property export limit")
			}
			off, ok := offsets[key]
			if !ok {
				off = uint32(table.Len())
				offsets[key] = off
				table.WriteString(key)
				table.WriteByte(0)
			}
			put(3)
			put(uint32(len(b)))
			put(off)
			structure.Write(b)
			pad()
		}
		for _, child := range nodes {
			if child.parent == n {
				if e := writeNode(child); e != nil {
					return e
				}
			}
		}
		put(2)
		return nil
	}
	if e = writeNode(nodes[0]); e != nil {
		return nil, "", 0, e
	}
	put(9)
	// Preserve no original reserve header bytes: regenerate the reserve map from
	// validated numeric address/size pairs, which describe hardware memory only.
	ro := be.Uint32(raw[16:])
	reserve := bytes.Buffer{}
	for pos := uint64(ro); ; pos += 16 {
		b := raw[pos : pos+16]
		reserve.Write(b)
		if be.Uint64(b) == 0 && be.Uint64(b[8:]) == 0 {
			break
		}
	}
	h := make([]byte, 40)
	be.PutUint32(h, fdtMagic)
	so := uint32(40 + reserve.Len())
	st := so + uint32(structure.Len())
	be.PutUint32(h[4:], st+uint32(table.Len()))
	be.PutUint32(h[8:], so)
	be.PutUint32(h[12:], st)
	be.PutUint32(h[16:], 40)
	be.PutUint32(h[20:], 17)
	be.PutUint32(h[24:], 16)
	be.PutUint32(h[32:], uint32(table.Len()))
	be.PutUint32(h[36:], uint32(structure.Len()))
	out := append(h, reserve.Bytes()...)
	out = append(out, structure.Bytes()...)
	out = append(out, table.Bytes()...)
	safeNodes, e := parseFDT(out)
	if e != nil {
		return nil, "", 0, e
	}
	dts := bytes.Buffer{}
	dts.WriteString("/dts-v1/;\n/* Source DTB sanitized for public hardware support. See SOURCES.json. */\n")
	var printNode func(*dtNode, int)
	printNode = func(n *dtNode, depth int) {
		indent := strings.Repeat("  ", depth)
		name := filepath.Base(n.path)
		if n.parent == nil {
			name = "/"
		}
		fmt.Fprintf(&dts, "%s%s {\n", indent, name)
		for _, k := range sortedPropertyKeys(n.props) {
			fmt.Fprintf(&dts, "%s  %s", indent, k)
			b := n.props[k]
			if len(b) > 0 {
				dts.WriteString(" = [")
				for _, v := range b {
					fmt.Fprintf(&dts, "%02x ", v)
				}
				dts.WriteString("]")
			}
			dts.WriteString(";\n")
		}
		for _, c := range safeNodes {
			if c.parent == n {
				printNode(c, depth+1)
			}
		}
		fmt.Fprintf(&dts, "%s};\n", indent)
	}
	printNode(safeNodes[0], 0)
	return out, dts.String(), removed, nil
}
func binaryWrite(w io.Writer, n uint32) error {
	b := make([]byte, 4)
	be.PutUint32(b, n)
	_, e := w.Write(b)
	return e
}
func sortedPropertyKeys(m map[string][]byte) []string {
	ks := []string{}
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// ExportBoardReport is the automatic unsupported/conflict path. It never exports
// partition contents, NV, user files, USB/Sahara identity, GUIDs, credentials or
// boot arguments. The DTB is a sanitized source dump, with original/export hashes
// and the sanitation scope recorded explicitly rather than calling it byte-exact.
func ExportBoardReport(ctx context.Context, d Reader, r HardwareReport, dir string, progress Emit) (string, error) {
	if e := checkDevice(ctx, d, r.Device); e != nil {
		return "", e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	files := map[string][]byte{}
	sources := []DTSource{}
	hashes := []PartitionHash{}
	notes := []string{}
	support := BoardSupport{Version: 1, StorageFingerprint: StorageFingerprint(r), Missing: []string{}}
	observed := []DTVariant{}
	exported := 0
	if r.Resolution.Status == "unsupported" && r.Resolution.Profile != "" {
		notes = append(notes, "A source profile was recognized; its installation inputs or firmware layout still require support. No private firmware/NV payloads are exported.")
	}
	safe := r
	// Installation attestations include private partition GUIDs. Public boot
	// diagnostics use the separate, allowlisted SUPPORT.json representation.
	safe.BootFirmware = nil
	safe.ActiveDTB = nil
	if strings.HasPrefix(r.InputFailure, "active-dtb-") || strings.HasPrefix(r.InputFailure, "storage-") {
		support.RequiredInputs = append([]string(nil), r.Resolution.Reasons...)
	}
	switch r.InputFailure {
	case "own-dsp":
		notes = append(notes, "Preparation failed while validating the attached device's own split DSP image. Board recognition succeeded.")
	case "own-wifi-calibration":
		notes = append(notes, "Preparation could not read the attached device's own Wi-Fi calibration. Board recognition succeeded.")
	default:
		safe.InputFailure = ""
	}
	safe.Device.Identity = Identity{}
	safe.AppliedSHA256 = ""
	safe.GPT.DiskGUID = ""
	safe.GPT.Partitions = append([]Partition(nil), r.GPT.Partitions...)
	safe.Variants = nil
	for i, p := range r.GPT.Partitions {
		name := publicPartitionName(p.Name, i)
		safe.GPT.Partitions[i].Name = name
		safe.GPT.Partitions[i].GUID = ""
		safe.GPT.Partitions[i].TypeGUID = ""
		if p.Name != "boot" && p.Name != "dtb" && p.Name != "dtbo" && p.Name != "vendor_boot" {
			continue
		}
		data, e := ReadRange(ctx, d, p.Range, 128<<20)
		if e != nil {
			support.Missing = append(support.Missing, name+":read-failed")
			continue
		}
		var variants []DTVariant
		if p.Name == "boot" {
			_, variants, e = BootDTBs(data)
		} else {
			variants, e = DTBs(data, p.Name)
		}
		if e != nil {
			support.Missing = append(support.Missing, name+":container-parse-failed")
		}
		observed = append(observed, variants...)
		hashes = append(hashes, PartitionHash{name, p.Range, HashBytes(data)})
		for pos := 0; pos+40 <= len(data); {
			idx := bytes.Index(data[pos:], []byte{0xd0, 0x0d, 0xfe, 0xed})
			if idx < 0 {
				break
			}
			pos += idx
			if pos+40 > len(data) {
				break
			}
			n := uint64(be.Uint32(data[pos+4:]))
			if n < 40 || n > 16<<20 || n > uint64(len(data)-pos) {
				notes = append(notes, fmt.Sprintf("Malformed DTB header in %s at %d: omitted", name, pos))
				pos += 4
				continue
			}
			raw := data[pos : pos+int(n)]
			b, _, removed, e := sanitizedDTB(raw)
			if e != nil {
				notes = append(notes, fmt.Sprintf("DTB in %s at %d could not be safely exported: omitted", name, pos))
				pos += int(n)
				continue
			}
			if len(sources) >= 256 {
				return "", errors.New("слишком много исходных DTB")
			}
			stem := fmt.Sprintf("stock/variant-%03d", len(sources))
			files[stem+".dtb"] = b
			src := DTSource{Source: name, Offset: uint64(pos), OriginalSHA256: HashBytes(raw), OriginalBytes: n, ExportSHA256: HashBytes(b), ExportBytes: len(b), RemovedProperties: removed}
			for _, variant := range variants {
				if variant.Source == p.Name && variant.Offset == uint64(pos) {
					src.Selectors = variant.Selectors
					src.OriginalMapSHA256 = variant.MapSHA256
					src.OriginalCompatibilitySHA256 = variant.CompatibilitySHA256
					src.BoardID = variant.BoardID
					src.InSourceSet = true
				}
			}
			sources = append(sources, src)
			v, e := ParseDTB(b, name, uint64(pos))
			if e == nil {
				v.Selectors = src.Selectors
				safe.Variants = append(safe.Variants, v)
				if src.InSourceSet {
					exported++
				}
			} else {
				notes = append(notes, "Source variant has unresolved bindings/phandles; sanitized dump retained")
			}
			pos += int(n)
		}
	}
	if len(r.Issues) > 0 || len(observed) == 0 || exported != len(observed) {
		support.Missing = append(support.Missing, "dt-source-set-incomplete")
	}
	if SourceSetFingerprint(observed) != SourceSetFingerprint(r.Variants) {
		support.Missing = append(support.Missing, "dt-source-set-changed")
	}
	support.DTSourcesComplete = len(support.Missing) == 0
	if support.DTSourcesComplete {
		support.SourceSetFingerprint = SourceSetFingerprint(r.Variants)
		support.CompatibilitySetFingerprint = CompatibilitySetFingerprint(r.Variants)
		support.AppliedDTBSHA256 = r.AppliedSHA256
	}
	if len(r.ActiveDTB) > 0 {
		clean, _, removed, err := sanitizedDTB(r.ActiveDTB)
		if err == nil {
			files["active.dtb"] = clean
			sources = append(sources, DTSource{Source: "active", OriginalSHA256: HashBytes(r.ActiveDTB), OriginalBytes: uint64(len(r.ActiveDTB)), ExportSHA256: HashBytes(clean), ExportBytes: len(clean), RemovedProperties: removed})
		} else {
			support.Missing = append(support.Missing, "active-dtb-export-failed")
		}
	}
	support.BootFirmware = reportBootFirmware(ctx, d, r)
	// Physical observations are stored separately from installation receipts.
	safe.Issues = nil
	if len(r.Issues) > 0 {
		safe.Issues = []string{fmt.Sprintf("%d source parsing/compatibility issue(s); private diagnostics omitted", len(r.Issues))}
	}
	safe.Resolution = Resolution{Status: r.Resolution.Status, Profile: r.Resolution.Profile, Candidates: r.Resolution.Candidates, Reasons: []string{"Public report: identity/GUIDs and private DT properties removed; inspect sanitized source variants"}}
	putJSON := func(name string, v any) error {
		b, e := json.MarshalIndent(v, "", "  ")
		if e == nil {
			files[name] = append(b, '\n')
		}
		return e
	}
	if e := putJSON("HardwareReport.json", safe); e != nil {
		return "", e
	}
	if e := putJSON("PARTITION-HASHES.json", hashes); e != nil {
		return "", e
	}
	if e := putJSON("SOURCES.json", sources); e != nil {
		return "", e
	}
	if e := putJSON("SUPPORT.json", support); e != nil {
		return "", e
	}
	_ = putJSON("NOTES.json", notes)
	files["README.txt"] = []byte("microstick hardware support report v1\nAttach this archive to an issue at https://github.com/microstick-os/microstick/issues.\nDTBs are sanitized copies of target source variants; original and export hashes, maps, board IDs and container selectors are in SOURCES.json. Only documented hardware property names are exported. Unknown properties and private/chosen properties are omitted.\nSUPPORT.json contains original registry fingerprints and numeric boot firmware diagnostics. dt_sources_complete refers only to source-set export integrity. Missing observations are listed with their diagnostic codes. Stored Secure Boot observations never authorize writes.\nNo partition payloads, calibration/NV, user files, hardware serials, MAC addresses, GUIDs, passwords or boot arguments are included.\n")
	sums := bytes.Buffer{}
	keys := []string{}
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&sums, "%s  %s\n", HashBytes(files[k]), k)
	}
	files["SHA256SUMS"] = sums.Bytes()
	keys = append(keys, "SHA256SUMS")
	sort.Strings(keys)
	f, e := os.CreateTemp(dir, ".board-report-")
	if e != nil {
		return "", e
	}
	defer os.Remove(f.Name())
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, k := range keys {
		b := files[k]
		if e = tw.WriteHeader(&tar.Header{Name: k, Mode: 0600, Size: int64(len(b)), ModTime: time.Unix(0, 0)}); e != nil {
			f.Close()
			return "", e
		}
		if _, e = tw.Write(b); e != nil {
			f.Close()
			return "", e
		}
	}
	if e = tw.Close(); e == nil {
		e = gz.Close()
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return "", e
	}
	if e = checkDevice(ctx, d, r.Device); e != nil {
		return "", e
	}
	out := filepath.Join(dir, "board-report-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".tar.gz")
	if e = os.Link(f.Name(), out); e != nil {
		return "", e
	}
	return out, syncDir(dir)
}
func publicPartitionName(name string, i int) string {
	switch name {
	case "sbl1", "sbl1bak", "aboot", "abootbak", "rpm", "rpmbak", "tz", "tzbak", "hyp", "hypbak", "modem", "boot", "recovery", "persist", "modemst1", "modemst2", "fsg", "fsc", "sec", "system", "userdata", "cache", "rootfs", "dtb", "dtbo", "vendor_boot", "misc", "config", "keystore", "devinfo", "oem", "ssd", "DDR":
		return name
	}
	return fmt.Sprintf("partition-%02d", i)
}
