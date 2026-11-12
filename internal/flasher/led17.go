package flasher

import (
	"bytes"
	"errors"
	"path/filepath"
)

const OriginalLinux17DTB = "6c0fb523f6a220794081c5866e39a84d3604d67d91ec68f376d1b7458766fa34"

// BuildOwnLED17 applies the HMUF02 RGB wiring to the pinned #17 DTB.
// It does not qualify a board/recipe, publish a bundle or access GPIO hardware.
// SIM nodes, their specifiers, defaults and the other pinctrl pins are retained.
func BuildOwnLED17(raw []byte) ([]byte, error) {
	registry, e := BuiltinRegistry()
	if e != nil {
		return nil, e
	}
	transform, ok := registry.payload.Transforms["hmuf02-rgb"]
	if !ok {
		return nil, errors.New("reviewed RGB transform unavailable")
	}
	return TransformDTB(raw, transform)
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
