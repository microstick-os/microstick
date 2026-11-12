package flasher

import (
	"bytes"
	"errors"
	"path/filepath"
	"strconv"
)

// TransformDTB edits only the listed GPIO specifiers and their pinctrl group.
// Both complete input and output images are bound to reviewed hashes.
func TransformDTB(raw []byte, t DTBTransform) ([]byte, error) {
	if HashBytes(raw) != t.SourceSHA256 || requireHash(t.ResultSHA256) != nil {
		return nil, errors.New("DTB transform source differs")
	}
	nodes, e := parseFDT(raw)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	groups := 0
	for _, n := range nodes {
		for _, c := range t.GPIOChanges {
			if n.path != c.Node {
				continue
			}
			if seen[c.Node] {
				return nil, errors.New("duplicate DTB GPIO change")
			}
			seen[c.Node] = true
			cs, e := cells(n.props["gpios"])
			if e != nil || len(cs) != 3 || cs[0] != c.Controller || cs[1] != c.From || cs[2] != c.Flags {
				return nil, errors.New("DTB GPIO source specifier changed")
			}
			be.PutUint32(n.props["gpios"][4:], c.To)
		}
		if filepath.Base(n.path) == t.PinGroup {
			groups++
			pins := stringsDT(n.props["pins"])
			for _, c := range t.GPIOChanges {
				from, to := fmtGPIO(c.From), fmtGPIO(c.To)
				count := 0
				for i, p := range pins {
					if p == from {
						pins[i] = to
						count++
					}
				}
				if count != 1 {
					return nil, errors.New("DTB pinctrl source changed")
				}
			}
			var b bytes.Buffer
			for _, pin := range pins {
				b.WriteString(pin)
				b.WriteByte(0)
			}
			n.props["pins"] = b.Bytes()
		}
	}
	if len(seen) != len(t.GPIOChanges) || groups != 1 {
		return nil, errors.New("incomplete DTB transformation")
	}
	out, e := encodeFDT(raw, nodes)
	if e != nil {
		return nil, e
	}
	if HashBytes(out) != t.ResultSHA256 {
		return nil, errors.New("DTB transformation result differs")
	}
	v, e := ParseDTB(out, "board-dtb", 0)
	if e != nil {
		return nil, e
	}
	if len(v.Issues) > 0 {
		return nil, errors.New("DTB transformation introduces resource conflicts")
	}
	return out, nil
}
func fmtGPIO(n uint32) string { return "gpio" + strconv.FormatUint(uint64(n), 10) }
func (p HardwareProfile) SelectDTB(raw []byte) ([]byte, error) {
	if HashBytes(raw) == p.Bundle["dtb"] {
		return raw, nil
	}
	if p.authorization == nil {
		return nil, errors.New("DTB requires a trusted profile")
	}
	id := p.authorization.variant.DTBTransform
	t, ok := p.authorization.snapshot.payload.Transforms[id]
	if !ok {
		return nil, errors.New("board DTB resource required")
	}
	return TransformDTB(raw, t)
}
func (p HardwareProfile) DTBResource() string {
	if p.authorization == nil {
		return ""
	}
	return p.authorization.variant.DTBResource
}
func (p HardwareProfile) WithFirmwareVersion(version string) HardwareProfile {
	if p.authorization != nil {
		a := *p.authorization
		a.firmwareVersion = version
		p.authorization = &a
	}
	return p
}
