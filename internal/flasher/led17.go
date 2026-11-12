package flasher

import (
	"bytes"
	"errors"
	"path/filepath"
)

const OriginalLinux17DTB = "6c0fb523f6a220794081c5866e39a84d3604d67d91ec68f376d1b7458766fa34"

// BuildOwnLED17 applies the HMUF02 RGB wiring to the pinned #17 DTB.
// SIM nodes, their specifiers, defaults and the other pinctrl pins are retained.
func BuildOwnLED17(raw []byte) ([]byte, error) {
	if HashBytes(raw) != OriginalLinux17DTB {
		return nil, errors.New("LED adaptation requires the exact reviewed #17 DTB")
	}
	nodes, e := parseFDT(raw)
	if e != nil {
		return nil, e
	}
	found := 0
	for _, n := range nodes {
		if p, ok := map[string]struct{ old, new uint32 }{"/leds/wifi": {20, 73}, "/leds/internet": {21, 71}, "/leds/os": {22, 72}}[n.path]; ok {
			cs, e := cells(n.props["gpios"])
			if e != nil || len(cs) != 3 || cs[0] != 0x3e || cs[1] != p.old || cs[2] != 0 {
				return nil, errors.New("LED source specifier changed")
			}
			be.PutUint32(n.props["gpios"][4:], p.new)
			found++
		}
		if filepath.Base(n.path) == "msmgpio-leds" {
			pins := stringsDT(n.props["pins"])
			counts := 0
			for i, s := range pins {
				switch s {
				case "gpio20":
					pins[i] = "gpio73"
					counts++
				case "gpio21":
					pins[i] = "gpio71"
					counts++
				case "gpio22":
					pins[i] = "gpio72"
					counts++
				}
			}
			if counts != 3 {
				return nil, errors.New("LED source pinctrl changed")
			}
			var b bytes.Buffer
			for _, s := range pins {
				b.WriteString(s)
				b.WriteByte(0)
			}
			n.props["pins"] = b.Bytes()
			found++
		}
	}
	if found != 4 {
		return nil, errors.New("incomplete LED adaptation")
	}
	out, e := encodeFDT(raw, nodes)
	if e != nil {
		return nil, e
	}
	v, e := ParseDTB(out, "own-led17-candidate", 0)
	if e != nil {
		return nil, e
	}
	if len(v.Issues) > 0 {
		return nil, errors.New("LED candidate introduces resource conflicts")
	}
	return out, nil
}
func encodeFDT(raw []byte, nodes []*dtNode) ([]byte, error) {
	table := bytes.Buffer{}
	structure := bytes.Buffer{}
	offsets := map[string]uint32{}
	put := func(n uint32) { _ = binaryWrite(&structure, n) }
	pad := func() {
		for structure.Len()%4 != 0 {
			structure.WriteByte(0)
		}
	}
	var node func(*dtNode)
	node = func(n *dtNode) {
		put(1)
		if n.parent != nil {
			structure.WriteString(filepath.Base(n.path))
		}
		structure.WriteByte(0)
		pad()
		for _, key := range sortedPropertyKeys(n.props) {
			b := n.props[key]
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
		for _, c := range nodes {
			if c.parent == n {
				node(c)
			}
		}
		put(2)
	}
	node(nodes[0])
	put(9)
	ro := be.Uint32(raw[16:])
	reserve := bytes.Buffer{}
	for pos := uint64(ro); ; pos += 16 {
		b := raw[pos : pos+16]
		reserve.Write(b)
		if be.Uint64(b) == 0 && be.Uint64(b[8:]) == 0 {
			break
		}
	}
	h := append([]byte(nil), raw[:40]...)
	so := uint32(40 + reserve.Len())
	st := so + uint32(structure.Len())
	be.PutUint32(h[4:], st+uint32(table.Len()))
	be.PutUint32(h[8:], so)
	be.PutUint32(h[12:], st)
	be.PutUint32(h[16:], 40)
	be.PutUint32(h[32:], uint32(table.Len()))
	be.PutUint32(h[36:], uint32(structure.Len()))
	out := append(h, reserve.Bytes()...)
	out = append(out, structure.Bytes()...)
	out = append(out, table.Bytes()...)
	return out, nil
}
