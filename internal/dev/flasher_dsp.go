package dev

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func ownWiFiCalibration(persist string) ([]byte, error) {
	for _, name := range []string{"/WCNSS_qcom_wlan_nv.bin", "/wlan/prima/WCNSS_qcom_wlan_nv.bin", "/wlan/WCNSS_qcom_wlan_nv.bin"} {
		data, e := imageBytes(persist, name)
		// The driver's NV format is supplied by this unit's matching WCNSS.
		// Accept calibration files within the supported size bounds.
		if e == nil && len(data) > 4 && len(data) <= 1<<20 {
			return data, nil
		}
	}
	return nil, errors.New("own Wi-Fi calibration unavailable")
}

// Read the connected device's Qualcomm split ELF, including its own segment
// list. Build versions and segment hashes identify input bytes, not a board.
// The hash table embedded in the MDT detects incomplete/mixed/corrupt inputs.
func extractOwnPIL(modem, root, stem string, machine uint16) ([]Entry, error) {
	mdt, e := command("", nil, "mtype", "-i", modem, "::/image/"+stem+".mdt")
	if e != nil {
		return nil, fmt.Errorf("не удалось прочитать родной %s MDT", stem)
	}
	le := binary.LittleEndian
	if len(mdt) < 52 || string(mdt[:4]) != "\x7fELF" || mdt[4] != 1 || mdt[5] != 1 || le.Uint16(mdt[18:]) != machine {
		return nil, fmt.Errorf("повреждён родной %s MDT", stem)
	}
	off, stride, count := uint64(le.Uint32(mdt[28:])), uint64(le.Uint16(mdt[42:])), uint64(le.Uint16(mdt[44:]))
	if stride != 32 || count < 2 || count > 100 || off < 52 || off+stride*count > uint64(len(mdt)) {
		return nil, fmt.Errorf("повреждена таблица сегментов %s", stem)
	}
	head := off + stride*count
	hashIndex := -1
	for i := uint64(0); i < count; i++ {
		p := mdt[off+i*stride:]
		if le.Uint32(p[24:])&(7<<24) == 2<<24 {
			if hashIndex >= 0 {
				return nil, fmt.Errorf("повреждён hash segment %s", stem)
			}
			hashIndex = int(i)
		}
	}
	if hashIndex < 0 || head+40+count*sha256.Size > uint64(len(mdt)) {
		return nil, fmt.Errorf("отсутствует таблица целостности %s", stem)
	}
	hashes := mdt[head:]
	if uint64(le.Uint32(hashes[20:])) != count*sha256.Size {
		return nil, fmt.Errorf("повреждена таблица целостности %s", stem)
	}
	var entries []Entry
	put := func(name string, data []byte) error {
		rel := "firmware/dsp/" + name
		if e := os.WriteFile(filepath.Join(root, rel), data, 0600); e != nil {
			return e
		}
		entries = append(entries, Entry{Name: name, Source: rel, Bytes: int64(len(data)), SHA256: digest(data)})
		return nil
	}
	for i := uint64(0); i < count; i++ {
		p := mdt[off+i*stride:]
		size := uint64(le.Uint32(p[16:]))
		if size == 0 {
			continue
		}
		if size > 128<<20 {
			return nil, fmt.Errorf("некорректный размер сегмента %s", stem)
		}
		name := fmt.Sprintf("%s.b%02d", stem, i)
		data, err := command("", nil, "mtype", "-i", modem, "::/image/"+name)
		if err != nil || uint64(len(data)) != size {
			return nil, fmt.Errorf("неполный родной DSP: %s", name)
		}
		switch {
		case int(i) == hashIndex:
			if !bytes.Equal(data, hashes) {
				return nil, fmt.Errorf("смешаны MDT и hash segment %s", stem)
			}
		default:
			h := sha256.Sum256(data)
			if !bytes.Equal(h[:], hashes[40+i*sha256.Size:40+(i+1)*sha256.Size]) {
				return nil, fmt.Errorf("нарушена целостность родного DSP: %s", name)
			}
		}
		if e = put(name, data); e != nil {
			return nil, e
		}
	}
	if e = put(stem+".mdt", mdt); e != nil {
		return nil, e
	}
	return entries, nil
}
