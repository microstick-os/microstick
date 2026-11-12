package flasher

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"io"
)

// CompactBootFirmwareELF removes only file padding and zero tails of PT_LOAD.
// p_memsz, addresses, flags and alignment remain unchanged: the loader must
// produce exactly the same memory image. This changes the signed ELF container
// and is therefore used ONLY after actual hardware proves Secure Boot disabled.
func CompactBootFirmwareELF(raw []byte) ([]byte, error) {
	le := binary.LittleEndian
	if len(raw) < 64 || string(raw[:4]) != "\x7fELF" || raw[4] != 2 || raw[5] != 1 || raw[6] != 1 || le.Uint16(raw[16:]) != 2 {
		return nil, errors.New("expected little-endian ELF64 firmware")
	}
	off := le.Uint64(raw[32:])
	size, count := uint64(le.Uint16(raw[54:])), uint64(le.Uint16(raw[56:]))
	if size != 56 || count == 0 || count > 128 || off > uint64(len(raw)) || size*count > uint64(len(raw))-off {
		return nil, errors.New("firmware ELF program table")
	}
	end := off + size*count
	out := append([]byte(nil), raw[:end]...)
	for i := uint64(0); i < count; i++ {
		p := raw[off+i*size : off+(i+1)*size]
		typ := le.Uint32(p)
		start, n, mem, va, align := le.Uint64(p[8:]), le.Uint64(p[32:]), le.Uint64(p[40:]), le.Uint64(p[16:]), le.Uint64(p[48:])
		if start > uint64(len(raw)) || n > uint64(len(raw))-start || typ == 1 && n > mem || align > 1<<20 || align > 0 && align&(align-1) != 0 {
			return nil, errors.New("firmware ELF segment bounds")
		}
		if i == 0 {
			if typ != 0 || start != 0 || n != end {
				return nil, errors.New("firmware ELF header segment")
			}
			continue
		}
		data := raw[start : start+n]
		if typ == 1 {
			data = bytes.TrimRight(data, "\x00")
		}
		if align == 0 {
			align = 1
		}
		pos := uint64(len(out))
		next := pos + (va%align+align-pos%align)%align
		if next > 2<<20 || uint64(len(data)) > 2<<20-next {
			return nil, errors.New("firmware exceeds staging bound")
		}
		out = append(out, make([]byte, next-pos)...)
		out = append(out, data...)
		le.PutUint64(out[off+i*size+8:], next)
		le.PutUint64(out[off+i*size+32:], uint64(len(data)))
	}
	out = append(out, make([]byte, (512-len(out)%512)%512)...)
	if e := verifyFirmwareMemory(raw, out); e != nil {
		return nil, e
	}
	return out, nil
}

func verifyFirmwareMemory(raw, compact []byte) error {
	a, e := elf.NewFile(bytes.NewReader(raw))
	if e != nil {
		return e
	}
	defer a.Close()
	b, e := elf.NewFile(bytes.NewReader(compact))
	if e != nil {
		return e
	}
	defer b.Close()
	if a.Entry != b.Entry || a.Machine != b.Machine || len(a.Progs) != len(b.Progs) {
		return errors.New("firmware ELF metadata changed")
	}
	for i, x := range a.Progs {
		y := b.Progs[i]
		if x.Type != y.Type {
			return errors.New("firmware ELF program type changed")
		}
		if x.Type != elf.PT_LOAD {
			continue
		}
		if x.Vaddr != y.Vaddr || x.Paddr != y.Paddr || x.Memsz != y.Memsz || x.Flags != y.Flags || x.Align != y.Align || x.Memsz > 4<<20 || x.Filesz > x.Memsz || y.Filesz > y.Memsz {
			return errors.New("firmware load metadata changed")
		}
		left, right := make([]byte, x.Memsz), make([]byte, y.Memsz)
		if _, e = io.ReadFull(x.Open(), left[:x.Filesz]); e != nil {
			return e
		}
		if _, e = io.ReadFull(y.Open(), right[:y.Filesz]); e != nil {
			return e
		}
		if !bytes.Equal(left, right) {
			return errors.New("firmware load memory changed")
		}
	}
	return nil
}
