package flasher

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var be = binary.BigEndian

const fdtMagic = 0xd00dfeed

type dtNode struct {
	path    string
	parent  *dtNode
	props   map[string][]byte
	enabled bool
}

func cells(p []byte) ([]uint32, error) {
	if len(p)%4 != 0 {
		return nil, errors.New("неполные FDT cells")
	}
	v := make([]uint32, len(p)/4)
	for i := range v {
		v[i] = be.Uint32(p[i*4:])
	}
	return v, nil
}
func stringsDT(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
}
func overlap(a, n, b, m uint32) bool {
	return uint64(a) < uint64(b)+uint64(m) && uint64(b) < uint64(a)+uint64(n)
}
func parseFDT(data []byte) ([]*dtNode, error) {
	if len(data) < 40 || be.Uint32(data) != fdtMagic {
		return nil, errors.New("FDT magic/header")
	}
	total, so, st, ro := be.Uint32(data[4:]), be.Uint32(data[8:]), be.Uint32(data[12:]), be.Uint32(data[16:])
	version, compat := be.Uint32(data[20:]), be.Uint32(data[24:])
	sn, sz := be.Uint32(data[32:]), be.Uint32(data[36:])
	if total < 40 || uint64(total) > uint64(len(data)) || version < 17 || version > 17 || compat > 17 || so%4 != 0 || ro%8 != 0 || so < 40 || st < 40 || ro < 40 || uint64(so)+uint64(sz) > uint64(total) || uint64(st)+uint64(sn) > uint64(total) || overlap(so, sz, st, sn) {
		return nil, errors.New("FDT bounds/version")
	}
	reservedEnd := uint64(ro)
	for {
		if reservedEnd+16 > uint64(total) {
			return nil, errors.New("FDT reserved-memory terminator")
		}
		a, n := be.Uint64(data[reservedEnd:]), be.Uint64(data[reservedEnd+8:])
		reservedEnd += 16
		if a == 0 && n == 0 {
			break
		}
		if a > ^uint64(0)-n {
			return nil, errors.New("FDT reserved overflow")
		}
	}
	if overlap(ro, uint32(reservedEnd)-ro, so, sz) || overlap(ro, uint32(reservedEnd)-ro, st, sn) {
		return nil, errors.New("FDT overlapping blocks")
	}
	structure := data[so : so+sz]
	table := data[st : st+sn]
	nodes := []*dtNode{}
	stack := []*dtNode{}
	paths := map[string]bool{}
	ended := false
	for pos := 0; pos < len(structure); {
		if pos+4 > len(structure) {
			return nil, errors.New("FDT truncated token")
		}
		token := be.Uint32(structure[pos:])
		pos += 4
		switch token {
		case 1:
			end := bytes.IndexByte(structure[pos:], 0)
			if end < 0 {
				return nil, errors.New("FDT node name")
			}
			name := string(structure[pos : pos+end])
			pos = (pos + end + 1 + 3) &^ 3
			if pos > len(structure) || strings.Contains(name, "/") || len(stack) > 128 || len(nodes) > 65536 {
				return nil, errors.New("FDT node bounds")
			}
			n := &dtNode{props: map[string][]byte{}, enabled: true}
			if len(stack) > 0 {
				n.parent = stack[len(stack)-1]
				n.path = strings.TrimSuffix(n.parent.path, "/") + "/" + name
			} else {
				if len(nodes) > 0 || name != "" {
					return nil, errors.New("FDT root")
				}
				n.path = "/"
			}
			if paths[n.path] {
				return nil, errors.New("FDT duplicate node")
			}
			paths[n.path] = true
			nodes = append(nodes, n)
			stack = append(stack, n)
		case 2:
			if len(stack) == 0 {
				return nil, errors.New("FDT unbalanced end")
			}
			stack = stack[:len(stack)-1]
		case 3:
			if len(stack) == 0 || pos+8 > len(structure) {
				return nil, errors.New("FDT property header")
			}
			ln, no := be.Uint32(structure[pos:]), be.Uint32(structure[pos+4:])
			pos += 8
			if uint64(pos)+uint64(ln) > uint64(len(structure)) || no >= uint32(len(table)) {
				return nil, errors.New("FDT property bounds")
			}
			end := bytes.IndexByte(table[no:], 0)
			if end <= 0 {
				return nil, errors.New("FDT string bounds")
			}
			name := string(table[no:uint32(int(no)+end)])
			n := stack[len(stack)-1]
			if _, ok := n.props[name]; ok {
				return nil, errors.New("FDT duplicate property")
			}
			n.props[name] = append([]byte(nil), structure[pos:pos+int(ln)]...)
			pos = (pos + int(ln) + 3) &^ 3
		case 4:
		case 9:
			if len(stack) != 0 || len(nodes) == 0 {
				return nil, errors.New("FDT unfinished tree")
			}
			ended = true
			pos = len(structure)
		default:
			return nil, errors.New("FDT unknown token")
		}
	}
	if !ended {
		return nil, errors.New("FDT END missing")
	}
	for _, n := range nodes {
		status := strings.TrimRight(string(n.props["status"]), "\x00")
		n.enabled = (status == "" || status == "okay" || status == "ok") && (n.parent == nil || n.parent.enabled)
	}
	return nodes, nil
}
func ParseDTB(data []byte, source string, offset uint64) (DTVariant, error) {
	var v DTVariant
	nodes, e := parseFDT(data)
	if e != nil {
		return v, e
	}
	nbytes := uint64(be.Uint32(data[4:]))
	v = DTVariant{Source: source, Offset: offset, Bytes: nbytes, SHA256: HashBytes(data[:nbytes])}
	v.Compatible = stringsDT(nodes[0].props["compatible"])
	v.BoardID, _ = cells(nodes[0].props["qcom,board-id"])
	handles := map[uint32]*dtNode{}
	for _, n := range nodes {
		var own uint32
		for _, key := range []string{"phandle", "linux,phandle"} {
			if b, ok := n.props[key]; ok {
				if len(b) != 4 || be.Uint32(b) == 0 || be.Uint32(b) == ^uint32(0) {
					return v, errors.New("FDT invalid phandle")
				}
				id := be.Uint32(b)
				if own != 0 && own != id {
					return v, errors.New("FDT phandle aliases disagree")
				}
				own = id
			}
		}
		if own != 0 {
			if _, ok := handles[own]; ok {
				return v, errors.New("FDT duplicate phandle")
			}
			handles[own] = n
		}
	}
	canonical := map[string]map[string]string{}
	activePins := map[string]string{}
	claim := func(key, owner, kind string) {
		if old, ok := activePins[key]; ok && old != owner && !strings.HasPrefix(old, owner+"/") && !strings.HasPrefix(owner, old+"/") {
			v.Issues = append(v.Issues, kind+" conflict: "+old+" / "+owner)
		}
		activePins[key] = owner
	}
	for _, n := range nodes {
		props := map[string]string{}
		for k, b := range n.props {
			props[k] = hex.EncodeToString(b)
		}
		canonical[n.path] = props
		keys := []string{}
		for k := range n.props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			b := n.props[key]
			// Qualcomm's downstream NIDnT binding stores a legacy GPIO
			// number, not a phandle specifier. Preserve it as unresolved
			// evidence rather than interpreting a coincidental handle.
			if key == "nidnt-gpio" {
				cs, err := cells(b)
				if err != nil {
					return v, err
				}
				v.Resources = append(v.Resources, Resource{n.path, key, "legacy-offset", cs, "", n.enabled})
				v.Issues = append(v.Issues, "Unqualified legacy GPIO binding: "+n.path+" "+key)
				continue
			}
			cellKey := ""
			kind := ""
			switch {
			case key == "gpios" || key == "gpio" || strings.HasSuffix(key, "-gpios") || strings.HasSuffix(key, "-gpio"):
				cellKey = "#gpio-cells"
				kind = "gpio"
			case key == "clocks":
				cellKey = "#clock-cells"
				kind = "ref"
			case key == "resets":
				cellKey = "#reset-cells"
				kind = "ref"
			case key == "interrupts-extended":
				cellKey = "#interrupt-cells"
				kind = "ref"
			case key == "power-domains":
				cellKey = "#power-domain-cells"
				kind = "ref"
			case key == "iommus":
				cellKey = "#iommu-cells"
				kind = "ref"
			case key == "phys":
				cellKey = "#phy-cells"
				kind = "ref"
			case strings.HasSuffix(key, "-supply") || key == "interrupt-parent":
				kind = "single"
			case strings.HasPrefix(key, "pinctrl-") && key != "pinctrl-names":
				if _, err := strconv.Atoi(strings.TrimPrefix(key, "pinctrl-")); err == nil {
					kind = "pinctrl"
				}
			}
			if kind != "" {
				cs, err := cells(b)
				if err != nil {
					return v, err
				}
				for i := 0; i < len(cs); {
					h, ok := handles[cs[i]]
					if !ok {
						return v, fmt.Errorf("FDT dangling phandle: %s %s", n.path, key)
					}
					i++
					nc := uint32(0)
					if cellKey != "" {
						p, ok := h.props[cellKey]
						if !ok || len(p) != 4 {
							return v, fmt.Errorf("FDT missing %s: %s %s -> %s", cellKey, n.path, key, h.path)
						}
						nc = be.Uint32(p)
						if nc > 16 {
							return v, errors.New("FDT cells limit")
						}
					}
					if int(nc) > len(cs)-i {
						return v, errors.New("FDT reference truncated")
					}
					args := append([]uint32(nil), cs[i:i+int(nc)]...)
					i += int(nc)
					state := ""
					if kind == "pinctrl" {
						index, err := strconv.Atoi(strings.TrimPrefix(key, "pinctrl-"))
						names := stringsDT(n.props["pinctrl-names"])
						if err != nil || index < 0 || index >= len(names) {
							state = fmt.Sprintf("unqualified-index-%d", index)
							v.Issues = append(v.Issues, "Unqualified pinctrl state: "+n.path+" "+key)
						} else {
							state = names[index]
						}
					}
					v.Resources = append(v.Resources, Resource{n.path, key, h.path, args, state, n.enabled})
					if kind == "gpio" && n.enabled && len(args) > 0 {
						pin := fmt.Sprintf("%s:gpio%d", h.path, args[0])
						claim(pin, n.path, "GPIO")
					}
					if kind == "pinctrl" && state == "default" && n.enabled {
						for _, group := range nodes {
							if group.path == h.path || strings.HasPrefix(group.path, h.path+"/") {
								for _, pin := range stringsDT(group.props["pins"]) {
									if pin == "" {
										continue
									}
									ctrl := h.parent
									for ctrl != nil {
										if _, ok := ctrl.props["gpio-controller"]; ok {
											break
										}
										ctrl = ctrl.parent
									}
									if ctrl == nil {
										continue
									}
									key := ctrl.path + ":" + pin
									claim(key, n.path, "pinctrl")
								}
							}
						}
					}
				}
				if kind == "single" && len(cs) != 1 {
					return v, errors.New("FDT single reference length")
				}
			} else if key == "interrupts" {
				var parent *dtNode
				for p := n; p != nil; p = p.parent {
					// An interrupt controller is the implicit interrupt parent of
					// its children, even when its own IRQ feeds another domain.
					if p != n {
						if _, ok := p.props["interrupt-controller"]; ok {
							parent = p
							break
						}
					}
					if b, ok := p.props["interrupt-parent"]; ok {
						if len(b) != 4 {
							return v, errors.New("FDT interrupt-parent")
						}
						parent = handles[be.Uint32(b)]
						break
					}
				}
				if parent == nil {
					return v, errors.New("FDT interrupts without parent")
				}
				b := parent.props["#interrupt-cells"]
				if len(b) != 4 || be.Uint32(b) == 0 {
					return v, errors.New("FDT interrupt cells")
				}
				cs, err := cells(n.props[key])
				if err != nil || len(cs)%int(be.Uint32(b)) != 0 {
					return v, fmt.Errorf("FDT interrupt arguments: %s", n.path)
				}
				v.Resources = append(v.Resources, Resource{n.path, key, parent.path, cs, "", n.enabled})
			} else if key == "reg" || key == "ranges" || key == "gpio-ranges" || key == "interrupt-map" || key == "interrupt-map-mask" || key == "memory-region" || key == "qcom,msm-id" || key == "qcom,board-id" || key == "qcom,pmic-id" {
				cs, err := cells(b)
				if err != nil {
					return v, err
				}
				v.Resources = append(v.Resources, Resource{n.path, key, "", cs, "", n.enabled})
				if key == "memory-region" {
					for _, id := range cs {
						if _, ok := handles[id]; !ok {
							return v, errors.New("FDT memory-region dangling phandle")
						}
					}
				}
			}
		}
	}
	v.MapSHA256 = Hash(canonical)
	// Compilation strings, boot arguments and per-unit addresses do not change
	// the board's wiring. Keep every other property, including unknown vendor
	// hardware bindings, rather than guessing that an unfamiliar delta is safe.
	for path, props := range canonical {
		for key := range props {
			if key == "serial-number" || key == "local-mac-address" || key == "mac-address" || path == "/" && key == "model" || path == "/chosen" && (key == "bootargs" || key == "stdout-path" || key == "linux,initrd-start" || key == "linux,initrd-end" || key == "kaslr-seed" || key == "rng-seed") {
				delete(props, key)
			}
		}
		if path == "/chosen" && len(props) == 0 {
			delete(canonical, path)
		}
	}
	v.CompatibilitySHA256 = Hash(canonical)
	return v, nil
}

// DTBs accepts raw/concatenated FDT, legacy Android boot (including appended
// QCDT), Android boot v1-v4 and Android DT table/DTBO containers. DTBO fixups
// are retained as variants; applying an overlay requires a profile recipe.
func DTBs(data []byte, source string) ([]DTVariant, error) {
	if len(data) < 8 || len(data) > 128<<20 {
		return nil, errors.New("DT image size")
	}
	if len(data) >= LK2ndBytes && HashBytes(data[:LK2ndBytes]) == LK2ndSHA256 || len(data) >= LK2ndOffset+8 && string(data[LK2ndOffset:LK2ndOffset+8]) == "ANDROID!" {
		_, vs, e := BootDTBs(data)
		for i := range vs {
			vs[i].Source = source
		}
		return vs, e
	}
	start, end := 0, len(data)
	switch {
	case string(data[:8]) == "VNDRBOOT":
		if len(data) < 2112 {
			return nil, errors.New("vendor_boot header truncated")
		}
		version, page := le.Uint32(data[8:]), uint64(le.Uint32(data[12:]))
		header := uint64(le.Uint32(data[2096:]))
		ram := uint64(le.Uint32(data[24:]))
		n := uint64(le.Uint32(data[2100:]))
		if (version != 3 && version != 4) || page < 512 || page > 65536 || page&(page-1) != 0 || header < 2112 || header > page || (version == 4 && header < 2128) {
			return nil, errors.New("vendor_boot geometry/version")
		}
		off := page + (ram+page-1)/page*page
		if off > uint64(len(data)) || n > uint64(len(data))-off || n < 40 {
			return nil, errors.New("vendor_boot DTB bounds")
		}
		vs, e := DTBs(data[off:off+n], source)
		if e != nil {
			return nil, e
		}
		for i := range vs {
			vs[i].Offset += off
		}
		return vs, nil
	case string(data[:8]) == "ANDROID!":
		if len(data) < 48 {
			return nil, errors.New("Android header truncated")
		}
		version := le.Uint32(data[40:])
		page := uint64(le.Uint32(data[36:]))
		ks, rs, ss := uint64(le.Uint32(data[8:])), uint64(le.Uint32(data[16:])), uint64(le.Uint32(data[24:]))
		if version == 3 || version == 4 {
			page = 4096
			ks = uint64(le.Uint32(data[8:]))
			rs = uint64(le.Uint32(data[12:]))
			ss = 0
		}
		if page < 512 || page > 65536 || page&(page-1) != 0 || ks == 0 {
			return nil, errors.New("Android page/kernel size")
		}
		align := func(n uint64) uint64 { return (n + page - 1) / page * page }
		required := page + align(ks) + align(rs) + align(ss)
		// Downstream Qualcomm uses this field as dt_size rather than a
		// header version. Its QCDT table is authoritative; searching the
		// compressed kernel for FDT magic also finds unrelated byte strings.
		if version > 4 {
			n := uint64(version)
			if required > uint64(len(data)) || n > uint64(len(data))-required {
				return nil, errors.New("Qualcomm DT section truncated")
			}
			section := data[required : required+n]
			if len(section) >= 4 && string(section[:4]) == "QCDT" {
				return qcdt(section, source, required)
			}
			vs, e := DTBs(section, source)
			if e != nil {
				return nil, e
			}
			for i := range vs {
				vs[i].Offset += required
			}
			return vs, nil
		}
		if version == 1 || version == 2 {
			if len(data) < 1648 || uint64(le.Uint32(data[1644:])) > page || le.Uint32(data[1644:]) < 1648 {
				return nil, errors.New("Android v1/v2 header")
			}
			n, off := uint64(le.Uint32(data[1632:])), le.Uint64(data[1636:])
			if n > 0 {
				if off < required || off > uint64(len(data)) || n > uint64(len(data))-off {
					return nil, errors.New("Android recovery DTBO bounds")
				}
				required = off + align(n)
			}
			if version == 2 {
				if len(data) < 1660 {
					return nil, errors.New("Android v2 header truncated")
				}
				required += align(uint64(le.Uint32(data[1648:])))
			}
		}
		if required > uint64(len(data)) {
			return nil, errors.New("Android payload truncated")
		}
		start = int(page)
		end = int(required)
	case be.Uint32(data) == 0xd7b7ab1e:
		if len(data) < 32 {
			return nil, errors.New("DT table header")
		}
		total, hs, es, count, off := be.Uint32(data[4:]), be.Uint32(data[8:]), be.Uint32(data[12:]), be.Uint32(data[16:]), be.Uint32(data[20:])
		if total > uint32(len(data)) || hs < 32 || hs > total || off < hs || es < 32 || count == 0 || count > 4096 || be.Uint32(data[28:]) != 0 || uint64(off)+uint64(count)*uint64(es) > uint64(total) {
			return nil, errors.New("DT table bounds")
		}
		out := []DTVariant{}
		for i := uint32(0); i < count; i++ {
			entry := data[off+i*es:]
			n, p := be.Uint32(entry), be.Uint32(entry[4:])
			if uint64(p)+uint64(n) > uint64(total) {
				return nil, errors.New("DT table entry bounds")
			}
			v, e := ParseDTB(data[p:p+n], source, uint64(p))
			if e != nil {
				return nil, e
			}
			if v.Bytes != uint64(n) {
				return nil, errors.New("DT table entry size")
			}
			out = append(out, v)
		}
		return out, nil
	case string(data[:4]) == "QCDT":
		return qcdt(data, source, 0)
	case be.Uint32(data) != fdtMagic:
		return nil, errors.New("неподдерживаемый boot/DTB/DTBO формат")
	}
	out := []DTVariant{}
	magic := []byte{0xd0, 0x0d, 0xfe, 0xed}
	for pos := start; pos < end; {
		index := bytes.Index(data[pos:end], magic)
		if index < 0 {
			break
		}
		pos += index
		v, e := ParseDTB(data[pos:], source, uint64(pos))
		if e != nil {
			return nil, fmt.Errorf("DTB offset %d: %w", pos, e)
		}
		out = append(out, v)
		pos += int(v.Bytes)
	}
	if len(out) == 0 {
		return nil, errors.New("в образе нет исходных DTB; требуется vendor_boot/dtb/dtbo")
	}
	return out, nil
}
func qcdt(data []byte, source string, base uint64) ([]DTVariant, error) {
	if len(data) < 12 {
		return nil, errors.New("QCDT header")
	}
	v, count := le.Uint32(data[4:]), le.Uint32(data[8:])
	size := uint32(0)
	switch v {
	case 1:
		size = 20
	case 2:
		size = 24
	case 3:
		size = 40
	default:
		return nil, errors.New("QCDT version")
	}
	if count > 4096 || uint64(count)*uint64(size)+12 > uint64(len(data)) {
		return nil, errors.New("QCDT entries")
	}
	out := []DTVariant{}
	indices := map[uint32]int{}
	for i := uint32(0); i < count; i++ {
		entry := data[12+i*size : 12+(i+1)*size]
		off, n := le.Uint32(entry[size-8:]), le.Uint32(entry[size-4:])
		if uint64(off)+uint64(n) > uint64(len(data)) {
			return nil, errors.New("QCDT range")
		}
		d, e := ParseDTB(data[off:off+n], source, base+uint64(off))
		if e != nil {
			return nil, e
		}
		selector, _ := cellsLittleEndian(entry[:size-8])
		if index, ok := indices[off]; ok {
			if out[index].Bytes != d.Bytes || out[index].SHA256 != d.SHA256 {
				return nil, errors.New("QCDT aliases disagree on size")
			}
			out[index].Selectors = append(out[index].Selectors, selector)
		} else {
			d.Selectors = [][]uint32{selector}
			indices[off] = len(out)
			out = append(out, d)
		}
	}
	return out, nil
}

func cellsLittleEndian(b []byte) ([]uint32, error) {
	if len(b)%4 != 0 {
		return nil, errors.New("QCDT selector cells")
	}
	v := make([]uint32, len(b)/4)
	for i := range v {
		v[i] = le.Uint32(b[i*4:])
	}
	return v, nil
}
