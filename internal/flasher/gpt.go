package flasher

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
	"unicode/utf16"
)

var le = binary.LittleEndian

func guid(b []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", le.Uint32(b), le.Uint16(b[4:]), le.Uint16(b[6:]), b[8:10], b[10:16])
}

type gptHeader struct {
	raw                                 []byte
	current, backup, first, last, array uint64
	count, size                         uint32
	entries                             []byte
}

func readHeader(ctx context.Context, d Reader, lba, total uint64) (gptHeader, error) {
	var h gptHeader
	b, e := ReadRange(ctx, d, Range{"user", lba * 512, 512}, 512)
	if e != nil {
		return h, e
	}
	if string(b[:8]) != "EFI PART" || le.Uint32(b[8:]) != 0x10000 || le.Uint32(b[20:]) != 0 {
		return h, errors.New("неверный GPT header")
	}
	n := le.Uint32(b[12:])
	if n < 92 || n > 512 {
		return h, errors.New("размер GPT header")
	}
	c := append([]byte(nil), b[:n]...)
	clear(c[16:20])
	if crc32.ChecksumIEEE(c) != le.Uint32(b[16:]) {
		return h, errors.New("GPT header CRC")
	}
	h = gptHeader{raw: b, current: le.Uint64(b[24:]), backup: le.Uint64(b[32:]), first: le.Uint64(b[40:]), last: le.Uint64(b[48:]), array: le.Uint64(b[72:]), count: le.Uint32(b[80:]), size: le.Uint32(b[84:])}
	if h.current != lba || h.backup >= total || h.backup == lba || h.first > h.last || h.last >= total || h.count == 0 || h.count > 4096 || h.size < 128 || h.size > 1024 || h.size%128 != 0 || bytes.Equal(b[56:72], make([]byte, 16)) {
		return h, errors.New("GPT geometry/GUID")
	}
	nbytes := uint64(h.count) * uint64(h.size)
	sectors := (nbytes + 511) / 512
	if h.array >= total || sectors > total-h.array {
		return h, errors.New("GPT array вне устройства")
	}
	if lba == 1 {
		if h.backup != total-1 || h.array < 2 || h.array+sectors > h.first {
			return h, errors.New("primary GPT placement")
		}
	} else {
		if lba != total-1 || h.backup != 1 || h.array <= h.last || h.array+sectors > lba {
			return h, errors.New("backup GPT placement")
		}
	}
	h.entries, e = ReadRange(ctx, d, Range{"user", h.array * 512, sectors * 512}, 4<<20)
	if e != nil {
		return h, e
	}
	h.entries = h.entries[:nbytes]
	if crc32.ChecksumIEEE(h.entries) != le.Uint32(b[88:]) {
		return h, errors.New("GPT array CRC")
	}
	return h, nil
}
func ReadGPT(ctx context.Context, d Reader, info DeviceInfo) (PartitionTable, error) {
	var out PartitionTable
	if e := ValidateInfo(info); e != nil {
		return out, e
	}
	total := info.Geometry.Areas["user"] / 512
	mbr, e := ReadRange(ctx, d, Range{"user", 0, 512}, 512)
	if e != nil {
		return out, e
	}
	if !bytes.Equal(mbr[510:512], []byte{0x55, 0xaa}) {
		return out, errors.New("MBR signature")
	}
	protect := false
	for i := 0; i < 4; i++ {
		typ := mbr[446+i*16+4]
		if typ == 0xee {
			protect = true
		} else if typ != 0 {
			return out, errors.New("hybrid MBR не поддерживается")
		}
	}
	if !protect {
		return out, errors.New("protective MBR отсутствует")
	}
	a, e := readHeader(ctx, d, 1, total)
	if e != nil {
		return out, e
	}
	b, e := readHeader(ctx, d, total-1, total)
	if e != nil {
		return out, e
	}
	if a.first != b.first || a.last != b.last || a.count != b.count || a.size != b.size || !bytes.Equal(a.raw[56:72], b.raw[56:72]) || !bytes.Equal(a.entries, b.entries) {
		return out, errors.New("GPT копии расходятся")
	}
	out.DiskGUID = guid(a.raw[56:72])
	out.Metadata = []Range{{"user", 0, a.first * 512}, {"user", (a.last + 1) * 512, (total - a.last - 1) * 512}}
	names := map[string]bool{}
	ids := map[string]bool{}
	for i := uint32(0); i < a.count; i++ {
		p := a.entries[i*a.size : (i+1)*a.size]
		if bytes.Equal(p[:16], make([]byte, 16)) {
			continue
		}
		first, last := le.Uint64(p[32:]), le.Uint64(p[40:])
		id := guid(p[16:32])
		if bytes.Equal(p[16:32], make([]byte, 16)) || ids[id] || first < a.first || last > a.last || first > last {
			return out, errors.New("GPT partition bounds/GUID")
		}
		ids[id] = true
		chars := []uint16{}
		for j := 56; j < 128; j += 2 {
			c := le.Uint16(p[j:])
			if c == 0 {
				break
			}
			if c >= 0xd800 && c <= 0xdfff {
				return out, errors.New("unsupported GPT UTF16")
			}
			chars = append(chars, c)
		}
		name := string(utf16.Decode(chars))
		if name == "" || strings.ContainsAny(name, "/\\\x00\n\r") || names[name] {
			return out, errors.New("GPT partition name")
		}
		names[name] = true
		out.Partitions = append(out.Partitions, Partition{name, guid(p[:16]), id, Range{"user", first * 512, (last - first + 1) * 512}})
	}
	sort.Slice(out.Partitions, func(i, j int) bool { return out.Partitions[i].Range.Offset < out.Partitions[j].Range.Offset })
	rs := []Range{}
	for _, p := range out.Partitions {
		rs = append(rs, p.Range)
	}
	if e = validateRanges(rs, info.Geometry); e != nil {
		return out, e
	}
	return out, nil
}
