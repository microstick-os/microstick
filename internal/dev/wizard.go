package dev

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf8"
)

type WizardOptions struct {
	RootCapacity   uint64
	InstallationID string
	Emit           flasher.Emit `json:"-"`
	BootFirmware   *flasher.BootFirmware
	SSID           string
	APPassword     string `json:"-"`
	RootPassword   string `json:"-"`
	RootPARTUUID   string
	OldRootfs      string
	Clean          bool
	Storage        *flasher.StorageRecipe
	OldData        string
	RequestBinding string
	Update         bool
	Release        *FlasherRelease
	// SelectedDTB is supplied by a reviewed recipe, never by a model picker.
	SelectedDTB []byte `json:"-"`
}
type wizardReceipt struct {
	PayloadSHA256 string                 `json:"payload_sha256,omitempty"`
	BootFirmware  *flasher.BootFirmware  `json:"boot_firmware,omitempty"`
	Version       int                    `json:"schema_version"`
	Fingerprint   string                 `json:"private_request_sha256"`
	Boot          string                 `json:"boot_sha256"`
	Rootfs        string                 `json:"rootfs_sha256"`
	Data          string                 `json:"data_sha256,omitempty"`
	Storage       *flasher.StorageRecipe `json:"storage,omitempty"`
	RecordSHA256  string                 `json:"installation_record_sha256"`
}

func ValidateWizardOptions(o WizardOptions) error {
	if o.Update {
		if o.Clean {
			return errors.New("обновление сохраняет данные")
		}
		if !regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`).MatchString(o.RootPARTUUID) {
			return errors.New("Не удалось определить раздел системы")
		}
		return nil
	}
	if !utf8.ValidString(o.SSID) || len(o.SSID) < 1 || len(o.SSID) > 32 || strings.ContainsAny(o.SSID, "\x00\r\n") {
		return &FormError{0, "Имя сети Wi-Fi: от 1 до 32 байт UTF-8, без перевода строки и нулевого символа"}
	}
	if len(o.APPassword) < 8 || len(o.APPassword) > 63 || strings.ContainsAny(o.APPassword, "\x00\r\n") {
		return &FormError{1, "Пароль Wi-Fi и панели: от 8 до 63 байт, без перевода строки и нулевого символа"}
	}
	if len(o.RootPassword) < 8 || len(o.RootPassword) > 128 || strings.ContainsAny(o.RootPassword, "\x00\r\n") {
		return &FormError{2, "Пароль root/SSH: от 8 до 128 байт, без перевода строки и нулевого символа"}
	}
	if !regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`).MatchString(o.RootPARTUUID) {
		return errors.New("Не удалось определить раздел системы")
	}
	return nil
}

// PrepareWizard personalizes a new private bundle atomically. Retry reuses both
// the installation identity and complete byte-verified output. Canonical inputs,
// host credentials and hardware/current are never modified.
func PrepareWizard(p Paths, images, state, output string, o WizardOptions) error {
	if e := ValidateWizardOptions(o); e != nil {
		return e
	}
	var e error
	output, e = privatePath(p.Repo, output)
	if e != nil {
		return e
	}
	state, e = privatePath(p.Repo, state)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(state), 0700); e != nil {
		return e
	}
	fd, e := syscall.Open(state+".wizard.lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer syscall.Close(fd)
	if e = syscall.Flock(fd, syscall.LOCK_EX); e != nil {
		return e
	}
	base, e := verifyManifest(images)
	if e != nil {
		return e
	}
	if base.Personalized {
		return errors.New("use an unpersonalized verified base")
	}
	inputHashes := map[string]string{}
	for _, n := range []string{"boot.img", "rootfs.ext4"} {
		inputHashes[n], e = hashFile(filepath.Join(images, n))
		if e != nil {
			return e
		}
	}
	hardwareHash, e := hashFile(filepath.Join(p.Hardware, "MANIFEST.json"))
	if e != nil {
		return e
	}
	oldHash := ""
	if o.OldRootfs != "" && !o.Clean {
		oldHash, e = hashFile(o.OldRootfs)
		if e != nil {
			return e
		}
	}
	oldDataHash := ""
	if o.Storage != nil {
		if o.RootPARTUUID != o.Storage.Root.GUID || o.RequestBinding == "" {
			return errors.New("storage/instance binding missing")
		}
		if o.Storage.DataMode == "preserve" {
			oldDataHash, e = hashFile(o.OldData)
			if e != nil {
				return e
			}
		}
	}
	raw, _ := json.Marshal([]any{o.InstallationID, o.BootFirmware, o.Storage, oldDataHash, o.RequestBinding, inputHashes, hardwareHash, o.SSID, o.APPassword, o.RootPassword, o.RootPARTUUID, o.Clean, oldHash, hex.EncodeToString(o.SelectedDTB), o.Update, o.Release})
	fingerprint := sha256.Sum256(raw)
	requestHash := hex.EncodeToString(fingerprint[:])
	receiptPath := state + ".wizard.json"
	if _, e = os.Stat(output); e == nil {
		var receipt wizardReceipt
		if e = readJSON(receiptPath, &receipt); e != nil {
			return e
		}
		if receipt.Version != 1 || receipt.Fingerprint != requestHash {
			return errors.New("retry request differs; use a new installation record")
		}
		if _, e = verifyManifest(output); e != nil {
			return e
		}
		recordHash, e := hashFile(state)
		if e != nil {
			return e
		}
		if recordHash != receipt.RecordSHA256 {
			return errors.New("retry installation record changed")
		}
		outputs := map[string]string{"boot.img": receipt.Boot, "rootfs.ext4": receipt.Rootfs}
		if receipt.Data != "" {
			outputs["data.ext4"] = receipt.Data
		}
		for n, want := range outputs {
			got, e := hashFile(filepath.Join(output, n))
			if e != nil {
				return e
			}
			if got != want {
				return errors.New("retry output changed")
			}
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(output), 0700); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(filepath.Dir(output), ".wizard-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	bundle := filepath.Join(stage, "bundle")
	if o.Update {
		e = prepareUpdateBase(images, state, bundle, o.OldRootfs)
	} else {
		e = Prepare(p, images, state, bundle, "", o.InstallationID)
	}
	if e != nil {
		return e
	}
	image := filepath.Join(bundle, "rootfs.ext4")
	if !o.Clean && o.OldRootfs != "" {
		// Stage only enough local capacity for real migrated files, then shrink again.
		// This does not determine the USB write length.
		oldUsed, e := filesystemAllocated(o.OldRootfs)
		if e != nil {
			return e
		}
		newUsed, e := filesystemAllocated(image)
		if e != nil {
			return e
		}
		need := uint64(oldUsed + newUsed + 16*1024*1024)
		need = (need + 4095) / 4096 * 4096
		st, e := os.Stat(image)
		if e != nil {
			return e
		}
		if need > uint64(st.Size()) {
			if o.RootCapacity > 0 && need > o.RootCapacity {
				return errors.New("для сохранённых данных недостаточно места")
			}
			if e = expandExt4(image, need); e != nil {
				return e
			}
		}
		if o.Emit != nil {
			o.Emit(flasher.Progress{Stage: "prepare-migration", Message: "Сохранение существующих настроек"})
		}
		if e = MigrateUserData(o.OldRootfs, image, stage); e != nil {
			return e
		}
	}
	if o.Emit != nil {
		o.Emit(flasher.Progress{Stage: "prepare-system", Message: "Подготовка системы"})
	}
	if e = injectWizardHardware(p, image, stage); e != nil {
		return e
	}
	put := func(path string, b []byte, mode int) error {
		local := filepath.Join(stage, "inject")
		if e := os.WriteFile(local, b, 0600); e != nil {
			return e
		}
		defer os.Remove(local)
		return imagePut(image, path, local, mode)
	}
	if o.Emit != nil {
		o.Emit(flasher.Progress{Stage: "prepare-settings", Message: "Применение настроек"})
	}
	var auth []byte
	if !o.Update {
		psk := hex.EncodeToString(pbkdf2([]byte(o.APPassword), []byte(o.SSID), 4096, 32, sha1.New))
		hostapd := "interface=wlan0\ndriver=nl80211\nctrl_interface=/run/hostapd\nssid=" + o.SSID + "\nhw_mode=g\nchannel=1\nieee80211n=1\nwmm_enabled=1\nauth_algs=1\nwpa=2\nwpa_key_mgmt=WPA-PSK\nrsn_pairwise=CCMP\nwpa_psk=" + psk + "\n"
		if e = put("/etc/microstick/hostapd.conf", []byte(hostapd), 0100600); e != nil {
			return e
		}
		saltBytes := make([]byte, 16)
		if _, e = rand.Read(saltBytes); e != nil {
			return e
		}
		salt := hex.EncodeToString(saltBytes)
		auth, _ = json.Marshal(map[string]string{"salt": salt, "hash": hex.EncodeToString(pbkdf2([]byte(o.APPassword), []byte(salt), 120000, 32, sha256.New))})
		for _, name := range []string{"/usr/share/microstick/default-plugins/web/default-auth.json", "/data/plugins/web/default-auth.json", "/data/plugins/web/.state/auth.json"} {
			if !imageExists(image, filepath.Dir(name)) {
				continue
			}
			if e = put(name, append(auth, '\n'), 0100600); e != nil {
				return e
			}
			if name == "/data/plugins/web/.state/auth.json" {
				for _, field := range []string{"uid 405", "gid 405"} {
					if _, e = debugImage(image, "set_inode_field "+name+" "+field, true); e != nil {
						return e
					}
				}
			}
		}
		shadow, e := imageBytes(image, "/etc/shadow")
		if e != nil {
			return e
		}
		hashed, e := command("", []byte(o.RootPassword+"\n"), "openssl", "passwd", "-6", "-salt", salt[:16], "-stdin")
		if e != nil {
			return errors.New("root password derivation failed")
		}
		lines := strings.Split(string(shadow), "\n")
		roots := 0
		for i, line := range lines {
			if strings.HasPrefix(line, "root:") {
				fields := strings.Split(line, ":")
				if len(fields) != 9 {
					return errors.New("base root shadow row malformed")
				}
				fields[1] = strings.TrimSpace(string(hashed))
				lines[i] = strings.Join(fields, ":")
				roots++
			}
		}
		if roots != 1 {
			return errors.New("base must have exactly one root account")
		}
		if e = put("/etc/shadow", []byte(strings.Join(lines, "\n")), 0100600); e != nil {
			return e
		}
	} else if e = preserveUpdateIdentity(o.OldRootfs, image, stage); e != nil {
		return e
	}
	if o.Update && o.Storage == nil {
		if e = refreshBuiltinPlugins(o.OldRootfs, image, o.OldRootfs, image, "/data/plugins", stage); e != nil {
			return e
		}
	}
	if o.Release != nil {
		b, _ := json.Marshal(o.Release)
		if e = put(installedReleasePath, append(b, '\n'), 0100644); e != nil {
			return e
		}
	}
	installedRecord, e := os.ReadFile(filepath.Join(bundle, "installation.json"))
	if e != nil {
		return e
	}
	if e = put("/usr/share/microstick/installation.json", installedRecord, 0100600); e != nil {
		return e
	}
	boot, e := os.ReadFile(filepath.Join(bundle, "boot.img"))
	if e != nil {
		return e
	}
	if e = verifyWizardKernel(p, boot); e != nil {
		return e
	}
	boot, e = personalizedBoot(boot, o.RootPARTUUID, o.SelectedDTB)
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(bundle, "boot.img"), boot, 0600); e != nil {
		return e
	}
	if o.Storage != nil {
		if e = prepareStockStorage(p, bundle, stage, o, append(auth, '\n')); e != nil {
			return e
		}
	}
	if e = stageBootFirmware(p, bundle, o.BootFirmware); e != nil {
		return e
	}
	if _, e = command("", nil, "e2fsck", "-fn", image); e != nil {
		return errors.New("personalized rootfs failed verification")
	}
	// Minimize the actual filesystem after all settings and migrations. Free
	// capacity is initialized locally on target, never expanded over USB.
	if o.Emit != nil {
		o.Emit(flasher.Progress{Stage: "prepare-package", Message: "Подготовка файлов для установки"})
	}
	if _, e = command("", nil, "resize2fs", "-M", image); e != nil {
		return e
	}
	if o.Storage != nil {
		st, e := os.Stat(image)
		if e != nil {
			return e
		}
		if uint64(st.Size()) > o.Storage.Root.Range.Length {
			return errors.New("система не помещается в раздел")
		}
	}
	mapper, _, _, e := FlasherBootstrap(p, false)
	if e != nil {
		return e
	}
	if e = flasher.CreatePayload(context.Background(), bundle, mapper); e != nil {
		return e
	}
	var m Manifest
	if e = readJSON(filepath.Join(bundle, "MANIFEST.json"), &m); e != nil {
		return e
	}
	if e = finalize(bundle, m); e != nil {
		return e
	}
	if _, e = verifyManifest(bundle); e != nil {
		return e
	}
	receipt := wizardReceipt{Version: 1, Fingerprint: requestHash, Storage: o.Storage, BootFirmware: o.BootFirmware}
	receipt.RecordSHA256, e = hashFile(state)
	if e != nil {
		return e
	}
	receipt.PayloadSHA256, e = hashFile(filepath.Join(bundle, "Payload.json"))
	if e != nil {
		return e
	}
	receipt.Boot, e = hashFile(filepath.Join(bundle, "boot.img"))
	if e != nil {
		return e
	}
	receipt.Rootfs, e = hashFile(image)
	if e != nil {
		return e
	}
	if e = writeJSONAtomic(filepath.Join(bundle, "Preparation.json"), receipt); e != nil {
		return e
	}
	if e = finalize(bundle, m); e != nil {
		return e
	}
	if e = writeJSONAtomic(receiptPath, receipt); e != nil {
		return e
	}
	if e = os.Rename(bundle, output); e != nil {
		return e
	}
	return nil
}
func injectWizardHardware(p Paths, image, temp string) error {
	var manifest Manifest
	if e := readJSON(filepath.Join(p.Hardware, "MANIFEST.json"), &manifest); e != nil {
		return e
	}
	if e := verifyEntries(p.Hardware, manifest.Artifacts); e != nil {
		return e
	}
	var profile Profile
	if e := readJSON(filepath.Join(p.Hardware, "profile.json"), &profile); e != nil {
		return e
	}
	if profile.KernelBuild != 17 || profile.KernelRelease != release {
		return errors.New("only exact kernel #17 is supported")
	}
	var fm FirmwareMap
	if e := readJSON(filepath.Join(p.Hardware, "derived/firmware-map.json"), &fm); e != nil {
		return e
	}
	for _, v := range fm.Files {
		if v.Component != "wifi-device-NV" {
			continue
		}
		src, e := safeJoin(p.Hardware, v.Artifact)
		if e != nil {
			return e
		}
		if !strings.HasPrefix(strings.TrimPrefix(v.Target, "/"), "lib/firmware/") || strings.Contains(v.Target, "..") {
			return errors.New("invalid calibration target")
		}
		if e = imagePut(image, "/"+strings.TrimPrefix(v.Target, "/"), src, 0100644); e != nil {
			return e
		}
	}
	var dsp DSP
	if e := readJSON(filepath.Join(p.Hardware, "dsp-firmware.json"), &dsp); e != nil {
		return e
	}
	// A stock version can have a different segment list. Remove previous base
	// segments so remoteproc always receives one complete own-device image.
	children, e := imageChildren(image, "/lib/firmware")
	if e != nil {
		return e
	}
	for _, child := range children {
		name := filepath.Base(child.Path)
		if strings.HasPrefix(name, "modem.") || strings.HasPrefix(name, "wcnss.") {
			if e = removeImageTree(image, child.Path, 0); e != nil {
				return e
			}
		}
	}
	for _, v := range dsp.Files {
		if filepath.Base(v.Name) != v.Name {
			return errors.New("invalid firmware name")
		}
		src, e := safeJoin(p.Hardware, v.Source)
		if e != nil {
			return e
		}
		h, e := hashFile(src)
		if e != nil {
			return e
		}
		if h != v.SHA256 {
			return errors.New("firmware checksum mismatch")
		}
		if v.Name == "mba.mbn" && h != "bcd6100886b10d0d36ce17b1ae70fdc4fd429ab3a0337e0b7cef9aa9b04b60c4" {
			return errors.New("verified runtime MBA required")
		}
		if e = imagePut(image, "/lib/firmware/"+v.Name, src, 0100644); e != nil {
			return e
		}
	}
	// A base built for another kernel/module set must be rebuilt, not patched by
	// release string. Check every selected module against this exact bundle.
	prefix := "kernel/kernel-and-modules-tree/lib/modules/" + release
	modules, e := ModuleSelection(filepath.Join(p.Hardware, prefix))
	if e != nil {
		return e
	}
	for _, name := range modules {
		want, e := os.ReadFile(filepath.Join(p.Hardware, prefix, name))
		if e != nil {
			return e
		}
		got, e := imageBytes(image, "/lib/modules/"+release+"/"+name)
		if e != nil || !bytes.Equal(got, want) {
			return errors.New("base module ABI/payload differs from selected hardware bundle")
		}
	}
	guard := "/usr/share/microstick/modem-guard.sha256"
	if len(dsp.Guard) > 0 {
		matched := false
		for _, v := range dsp.Files {
			if v.Name == dsp.Guard["firmware_file"] && v.SHA256 == dsp.Guard["firmware_sha256"] {
				matched = true
			}
		}
		if !matched || dsp.Guard["item"] != float64(2500) || dsp.Guard["marker"] != "HiMI_OK" || dsp.Guard["write_only_when_zero"] != true {
			return errors.New("unreviewed RAM-shadow guard")
		}
		b := []byte(dsp.Guard["firmware_sha256"].(string) + "  /lib/firmware/" + dsp.Guard["firmware_file"].(string) + "\n")
		local := filepath.Join(temp, "guard")
		if e = os.WriteFile(local, b, 0600); e != nil {
			return e
		}
		defer os.Remove(local)
		if e = imagePut(image, guard, local, 0100644); e != nil {
			return e
		}
	} else if e = removeImageTree(image, guard, 0); e != nil {
		return e
	}
	return nil
}
func personalizedBoot(boot []byte, root string, dtb []byte) ([]byte, error) {
	if len(boot) < 2048 || string(boot[:8]) != "ANDROID!" || binary.LittleEndian.Uint32(boot[36:]) != 2048 {
		return nil, errors.New("unsupported #17 boot layout")
	}
	ks, rs := uint64(binary.LittleEndian.Uint32(boot[8:])), uint64(binary.LittleEndian.Uint32(boot[16:]))
	ramOff := uint64(2048) + (ks+2047)/2048*2048
	if 2048+ks > uint64(len(boot)) || ramOff+rs > uint64(len(boot)) {
		return nil, errors.New("truncated boot")
	}
	cmd := strings.TrimRight(string(boot[64:576]), "\x00")
	m := regexp.MustCompile(`root=PARTUUID=([0-9a-fA-F-]{36})`).FindStringSubmatch(cmd)
	if len(m) != 2 {
		return nil, errors.New("boot PARTUUID missing")
	}
	gz, e := gzip.NewReader(bytes.NewReader(boot[ramOff : ramOff+rs]))
	if e != nil {
		return nil, e
	}
	raw, e := io.ReadAll(io.LimitReader(gz, 16<<20))
	gz.Close()
	if e != nil {
		return nil, e
	}
	if !bytes.Contains(raw, []byte(m[1])) {
		return nil, errors.New("initramfs PARTUUID missing")
	}
	raw = bytes.ReplaceAll(raw, []byte(m[1]), []byte(root))
	var ram bytes.Buffer
	z := gzip.NewWriter(&ram)
	if _, e = z.Write(raw); e != nil {
		return nil, e
	}
	if e = z.Close(); e != nil {
		return nil, e
	}
	kernel := boot[2048 : 2048+ks]
	if len(dtb) > 0 {
		if len(dtb) < 40 || binary.BigEndian.Uint32(dtb) != 0xd00dfeed {
			return nil, errors.New("selected DTB invalid")
		}
		r := bytes.NewReader(kernel)
		z, e := gzip.NewReader(r)
		if e != nil {
			return nil, e
		}
		z.Multistream(false)
		if _, e = io.Copy(io.Discard, z); e != nil {
			return nil, e
		}
		z.Close()
		kernel = append(append([]byte(nil), kernel[:len(kernel)-r.Len()]...), dtb...)
	}
	layout := BootLayout{KernelAddress: fmtHex(binary.LittleEndian.Uint32(boot[12:])), RamdiskAddress: fmtHex(binary.LittleEndian.Uint32(boot[20:])), TagsAddress: fmtHex(binary.LittleEndian.Uint32(boot[32:])), Page: 2048, CommandLine: strings.ReplaceAll(cmd, m[1], root)}
	layout.CommandLine = strings.TrimSuffix(layout.CommandLine, " rootwait rootfstype=ext4 ipv6.disable=1")
	return MakeBoot(kernel, ram.Bytes(), layout)
}
func fmtHex(v uint32) string { return fmt.Sprintf("0x%x", v) }

// VerifyOwnWizardInputs compares the selected bundle with fresh target modem and
// persist reads. Private filenames/contents are never returned in diagnostics.
func VerifyOwnWizardInputs(p Paths, modem, persist string, dtb []byte) (map[string]string, error) {
	var m Manifest
	if e := readJSON(filepath.Join(p.Hardware, "MANIFEST.json"), &m); e != nil {
		return nil, e
	}
	if e := verifyEntries(p.Hardware, m.Artifacts); e != nil {
		return nil, e
	}
	result := map[string]string{}
	hash := func(v any) string { b, _ := json.Marshal(v); h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	kernel, e := hashFile(filepath.Join(p.Hardware, "boot/kernel.gz"))
	if e != nil {
		return nil, e
	}
	result["kernel"] = kernel
	if len(dtb) == 0 {
		dtb, e = os.ReadFile(filepath.Join(p.Hardware, "device-tree/boot-image.dtb"))
		if e != nil {
			return nil, e
		}
	}
	dh := sha256.Sum256(dtb)
	result["dtb"] = hex.EncodeToString(dh[:])
	mods := map[string]string{}
	prefix := "kernel/kernel-and-modules-tree/lib/modules/" + release
	selected, e := ModuleSelection(filepath.Join(p.Hardware, prefix))
	if e != nil {
		return nil, e
	}
	for _, name := range selected {
		h, e := hashFile(filepath.Join(p.Hardware, prefix, name))
		if e != nil {
			return nil, e
		}
		mods[name] = h
	}
	result["modules"] = hash(mods)
	temp, e := os.MkdirTemp(filepath.Dir(modem), ".own-input-check-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(temp)
	var dsp DSP
	if e = readJSON(filepath.Join(p.Hardware, "dsp-firmware.json"), &dsp); e != nil {
		return nil, e
	}
	mpss, wcnss := map[string]string{}, map[string]string{}
	for _, f := range dsp.Files {
		if f.Name == "mba.mbn" {
			if f.SHA256 != "bcd6100886b10d0d36ce17b1ae70fdc4fd429ab3a0337e0b7cef9aa9b04b60c4" {
				return nil, errors.New("verified runtime MBA required")
			}
			result["mba"] = f.SHA256
			continue
		}
		if filepath.Base(f.Name) != f.Name {
			return nil, errors.New("invalid DSP filename")
		}
		out := filepath.Join(temp, "part")
		os.Remove(out)
		if _, e = command("", nil, "mcopy", "-i", modem, "::/image/"+f.Name, out); e != nil {
			return nil, errors.New("fresh own-device DSP extraction failed")
		}
		h, e := hashFile(out)
		if e != nil {
			return nil, e
		}
		if h != f.SHA256 {
			return nil, errors.New("selected DSP differs from own target modem")
		}
		switch {
		case strings.HasPrefix(f.Name, "modem."):
			mpss[f.Name] = h
		case strings.HasPrefix(f.Name, "wcnss."):
			wcnss[f.Name] = h
		default:
			return nil, errors.New("unreviewed DSP family")
		}
	}
	if len(mpss) == 0 || len(wcnss) == 0 || result["mba"] == "" {
		return nil, errors.New("incomplete DSP bundle")
	}
	result["mpss"], result["wcnss"] = hash(mpss), hash(wcnss)
	own, e := ownWiFiCalibration(persist)
	if e != nil {
		return nil, errors.New("fresh own Wi-Fi calibration unavailable")
	}
	var fm FirmwareMap
	if e = readJSON(filepath.Join(p.Hardware, "derived/firmware-map.json"), &fm); e != nil {
		return nil, e
	}
	matched := false
	for _, f := range fm.Files {
		if f.Component == "wifi-device-NV" {
			file, e := safeJoin(p.Hardware, f.Artifact)
			if e != nil {
				return nil, e
			}
			b, e := os.ReadFile(file)
			if e != nil {
				return nil, e
			}
			if !bytes.Equal(b, own) {
				return nil, errors.New("selected Wi-Fi NV belongs to different hardware inputs")
			}
			matched = true
		}
	}
	if !matched {
		return nil, errors.New("no reviewed own Wi-Fi NV mapping")
	}
	return result, nil
}

func FinalizeWizardAttestation(output string, attestation any) error {
	if _, e := verifyManifest(output); e != nil {
		return e
	}
	if e := writeJSONAtomic(filepath.Join(output, "HardwareInputs.json"), attestation); e != nil {
		return e
	}
	var m Manifest
	if e := readJSON(filepath.Join(output, "MANIFEST.json"), &m); e != nil {
		return e
	}
	return finalize(output, m)
}

func verifyWizardKernel(p Paths, boot []byte) error {
	if len(boot) < 2048 || string(boot[:8]) != "ANDROID!" || binary.LittleEndian.Uint32(boot[36:]) != 2048 {
		return errors.New("unsupported base boot")
	}
	size := uint64(binary.LittleEndian.Uint32(boot[8:]))
	if size > uint64(len(boot))-2048 {
		return errors.New("truncated base kernel")
	}
	kernel := boot[2048 : 2048+size]
	reader := bytes.NewReader(kernel)
	z, e := gzip.NewReader(reader)
	if e != nil {
		return e
	}
	z.Multistream(false)
	if _, e = io.Copy(io.Discard, z); e != nil {
		z.Close()
		return e
	}
	if e = z.Close(); e != nil {
		return e
	}
	expected, e := os.ReadFile(filepath.Join(p.Hardware, "boot/kernel.gz"))
	if e != nil {
		return e
	}
	if !bytes.Equal(kernel[:len(kernel)-reader.Len()], expected) {
		return errors.New("base kernel payload differs from exact hardware kernel #17")
	}
	return nil
}
