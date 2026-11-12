package flasher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Recipes require reviewed code and exact inputs. Software support and physical
// verification of a new installer run are recorded separately.
// A JSON profile cannot by itself enable an arbitrary range or live write.
func recipeAllowed(p InstallPlan) bool {
	return p.Device.Identity.Kind == "simulator" && p.Recipe == "simulator-v1" || p.Mode == "restore" && p.Recipe == "own-backup-v1" || (nativeIdentity(p.Device.Identity) && p.Recipe == "preserve-gpt-v1" || p.Recipe == StockRecipe) && p.Profile != ""
}
func ConfirmNoBackup(answer string) bool {
	return strings.TrimSpace(answer) == "y" || strings.TrimSpace(answer) == "Y"
}
func BuildInstallPlan(ctx context.Context, d Reader, r HardwareReport, images string, quickAnswer string, backup string, mapper ...string) (InstallPlan, error) {
	p := InstallPlan{Version: Schema, Device: r.Device, Profile: r.Resolution.Profile, Recipe: "simulator-v1", Mode: "install"}
	if e := requireMatched(r); e != nil {
		return p, e
	}
	if r.Device.Identity.Kind != "simulator" {
		return p, ErrUnsupported
	}
	return buildInstallPlan(ctx, d, r, images, quickAnswer, backup, p, mapper...)
}

// BuildQualifiedInstallPlan re-resolves a reviewed profile, verifies the image
// attestation against its exact bundle, and permits only the implemented
// no-repartition boot/rootfs or reviewed stock boot/system/userdata recipe.
func BuildQualifiedInstallPlan(ctx context.Context, d Reader, r HardwareReport, profile HardwareProfile, images, quickAnswer, backup string, mapper ...string) (InstallPlan, error) {
	if !canonicalNativeProfile(profile) {
		return InstallPlan{}, ErrUnsupported
	}
	if profile.Recipe == StockRecipe {
		return buildStockPlan(ctx, d, r, profile, images, quickAnswer, backup, mapper...)
	}
	r.Resolution = Resolve(r, []HardwareProfile{profile})
	p := InstallPlan{Version: Schema, Device: r.Device, Profile: profile.ID, Recipe: profile.Recipe, Mode: "install"}
	if e := requireMatched(r); e != nil {
		return p, e
	}
	if !nativeIdentity(r.Device.Identity) || profile.Recipe != "preserve-gpt-v1" {
		return p, ErrUnsupported
	}
	p.qualification = &profile
	var att ImageAttestation
	if e := readJSON(filepath.Join(images, "HardwareInputs.json"), &att); e != nil {
		return p, e
	}
	if att.Version != Schema || !EqualDevice(att.Device, r.Device) || att.Profile != profile.ID || !CompatibleBundle(profile, att.Bundle) {
		return p, errors.New("подготовленные аппаратные входы отличаются от профиля/экземпляра")
	}
	if e := bindBootFirmware(ctx, d, r, profile, att, images, &p); e != nil {
		return p, e
	}
	var e error
	p.InstallationSHA256, e = HashFile(filepath.Join(images, "Preparation.json"))
	if e != nil {
		return p, e
	}
	if p.InstallationSHA256 != att.ReceiptSHA256 {
		return p, errors.New("installation receipt изменён")
	}
	for _, name := range []string{"boot.img", "rootfs.ext4", "Payload.json"} {
		h, e := HashFile(filepath.Join(images, name))
		if e != nil {
			return p, e
		}
		if h != att.Images[name] {
			return p, errors.New("образы изменились после аппаратной персонализации")
		}
	}
	return buildInstallPlan(ctx, d, r, images, quickAnswer, backup, p, mapper...)
}

type ImageAttestation struct {
	BootFirmware  *BootFirmware     `json:"boot_firmware,omitempty"`
	Version       int               `json:"schema_version"`
	Device        DeviceInfo        `json:"device"`
	Profile       string            `json:"profile"`
	Bundle        map[string]string `json:"bundle_sha256"`
	Images        map[string]string `json:"image_sha256"`
	Storage       *StorageRecipe    `json:"storage,omitempty"`
	ReceiptSHA256 string            `json:"preparation_receipt_sha256,omitempty"`
}

func buildInstallPlan(ctx context.Context, d Reader, r HardwareReport, images, quickAnswer, backup string, p InstallPlan, mapper ...string) (InstallPlan, error) {
	if p.Storage != nil && p.Storage.DataMode == "format" {
		quickAnswer = ""
	}
	if quickAnswer != "" {
		if !ConfirmNoBackup(quickAnswer) {
			return p, errors.New("отказ от бэкапа не подтверждён")
		}
		p.Mode = "quick"
		p.BackupWaived = true
	} else {
		if e := VerifyBackup(ctx, backup, r.Device, mapper...); e != nil {
			return p, e
		}
		m, e := LoadBackup(backup)
		if e != nil {
			return p, e
		}
		expected, e := Coverage(r)
		if e != nil {
			return p, e
		}
		if Hash(expected) != Hash(sortedRanges(m.Coverage)) {
			return p, errors.New("бэкап не покрывает исходную разметку")
		}
		p.BackupSHA256, e = HashFile(filepath.Join(backup, "BackupManifest.json"))
		if e != nil {
			return p, e
		}
		p.Guards = append(p.Guards, m.Extents...)
	}
	if e := checkDevice(ctx, d, p.Device); e != nil {
		return p, e
	}
	if p.BackupWaived {
		rs, e := Coverage(r)
		if e != nil {
			return p, e
		}
		for _, x := range rs {
			h, e := HashRange(ctx, d, x)
			if e != nil {
				return p, e
			}
			p.Guards = append(p.Guards, Extent{Range: x, SHA256: h})
		}
	}
	var payload PayloadManifest
	if e := readJSON(filepath.Join(images, "Payload.json"), &payload); e == nil {
		if payload.Version != 1 {
			return p, errors.New("unsupported payload contract")
		}
		p.PayloadVersion = 1
		digest, err := HashFile(filepath.Join(images, "Payload.json"))
		if err != nil {
			return p, err
		}
		p.PayloadSHA256 = digest
	} else if nativeIdentity(p.Device.Identity) {
		return p, errors.New("prepared images use the old payload contract; restore own backup before starting a new installation")
	}
	pairs := [][2]string{{"rootfs", "rootfs.ext4"}, {"boot", "boot.img"}}
	if p.Storage != nil {
		pairs = [][2]string{{"system", "rootfs.ext4"}, {"boot", "boot.img"}}
	}
	if p.BootFirmware != nil && p.BootFirmware.Upgrade {
		last := pairs[len(pairs)-1]
		pairs = append(append(pairs[:len(pairs)-1:len(pairs)-1], p.BootFirmware.Files()...), last)
	}
	for _, pair := range pairs {
		var partition *Partition
		for i := range r.GPT.Partitions {
			if r.GPT.Partitions[i].Name == pair[0] {
				partition = &r.GPT.Partitions[i]
				break
			}
		}
		if partition == nil {
			return p, errors.New("рецепт не допускает изменения GPT: отсутствует boot/rootfs")
		}
		if pair[0] != "tz" && pair[0] != "hyp" {
			p.Targets = append(p.Targets, *partition)
		}
		path, e := safeFile(images, pair[1])
		if e != nil {
			return p, e
		}
		st, e := os.Stat(path)
		if e != nil {
			return p, e
		}
		rng := partition.Range
		rng.Length = uint64(st.Size())
		if !rng.Valid(p.Device.Geometry) || rng.Length > partition.Range.Length {
			return p, errors.New("образ не помещается в исходный раздел")
		}
		if p.PayloadVersion == 1 {
			if pair[1] == "rootfs.ext4" {
				if payload.RootBytes != uint64(st.Size()) || payload.RootSHA256 == "" {
					return p, errors.New("root payload descriptor mismatch")
				}
				for _, x := range payload.Extents {
					if x.Offset > rng.Length || x.Length > rng.Length-x.Offset {
						return p, errors.New("payload outside image")
					}
					p.Operations = append(p.Operations, Operation{Range: Range{rng.Area, rng.Offset + x.Offset, x.Length}, File: pair[1], FileOffset: x.Offset, SHA256: x.SHA256})
				}
			} else {
				h, e := HashFile(path)
				if e != nil {
					return p, e
				}
				p.Operations = append(p.Operations, Operation{Range: rng, File: pair[1], SHA256: h})
			}
			continue
		}
		h, e := HashFile(path)
		if e != nil {
			return p, e
		}

		source := d
		if backup != "" {
			snapshot, e := OpenSnapshot(d, backup)
			if e != nil {
				return p, e
			}
			source = snapshot
			// Compact backups intentionally omit free bytes. The native source
			// check still binds the whole write range to its current real bytes.
			for _, region := range snapshot.Manifest.Regions {
				if region.Mode != "full" && region.Range.Area == rng.Area && rng.Offset >= region.Range.Offset && rng.Offset+rng.Length <= region.Range.Offset+region.Range.Length {
					source = d
					break
				}
			}
		}
		before, e := HashRange(ctx, source, rng)
		if e != nil {
			return p, e
		}
		p.Operations = append(p.Operations, Operation{Range: rng, File: pair[1], SHA256: h, BeforeSHA256: before})
	}
	if p.PayloadVersion == 1 {
		// Only partition-table bytes are guards. Bounded transport cannot write outside
		// its grant; rereading every backed-up NV/data byte adds no address protection.
		var guards []Extent
		for _, g := range p.Guards {
			for _, meta := range r.GPT.Metadata {
				if g.Range == meta {
					guards = append(guards, g)
				}
			}
		}
		p.Guards = guards
	}
	p.Protected = protection(p.Guards, p.Operations)
	return p, ValidatePlan(p)
}
func BuildRestorePlan(ctx context.Context, d Reader, backup string, mapper ...string) (InstallPlan, error) {
	if native, ok := d.(*EDLReader); ok {
		m, e := LoadBackup(backup)
		if e != nil {
			return InstallPlan{}, e
		}
		if e = VerifyBackup(ctx, backup, m.Device, mapper...); e != nil {
			return InstallPlan{}, e
		}
		if e = native.BindOwnBackup(ctx, backup, m); e != nil {
			return InstallPlan{}, e
		}
	}
	info, e := d.Info(ctx)
	p := InstallPlan{Version: Schema, Device: info, Mode: "restore", Recipe: "own-backup-v1", PayloadVersion: 1}
	if e != nil {
		return p, e
	}
	if e = VerifyBackup(ctx, backup, info, mapper...); e != nil {
		return p, e
	}
	m, e := LoadBackup(backup)
	if e != nil {
		return p, e
	}
	p.BackupSHA256, e = HashFile(filepath.Join(backup, "BackupManifest.json"))
	if e != nil {
		return p, e
	}
	for _, x := range m.Extents {
		p.Operations = append(p.Operations, Operation{Range: x.Range, File: x.File, SHA256: x.SHA256})
	}
	return p, ValidatePlan(p)
}
func ValidatePlan(p InstallPlan) error {
	if p.PayloadVersion < 0 || p.PayloadVersion > 1 || p.Version != Schema || ValidateInfo(p.Device) != nil || !recipeAllowed(p) || len(p.Operations) == 0 {
		return errors.New("недопустимый план/рецепт")
	}
	if p.Mode != "install" && p.Mode != "update" && p.Mode != "quick" && p.Mode != "restore" {
		return errors.New("режим плана")
	}
	if p.BackupWaived != (p.Mode == "quick") {
		return errors.New("отказ от бэкапа разрешён только в быстром режиме")
	}
	if !p.BackupWaived && requireHash(p.BackupSHA256) != nil {
		return errors.New("нет привязки к бэкапу")
	}
	if p.Recipe == StockRecipe {
		if e := validateStockPlan(p); e != nil {
			return e
		}
	}
	if e := validateBootFirmwarePlan(p); e != nil {
		return e
	}
	if p.Mode != "restore" && nativeIdentity(p.Device.Identity) {
		if e := validateNativeTargets(p); e != nil {
			return e
		}
	}
	rs := []Range{}
	for _, op := range p.Operations {
		if requireHash(op.SHA256) != nil || p.PayloadVersion == 0 && requireHash(op.BeforeSHA256) != nil {
			return errors.New("нет контрольных SHA256")
		}
		rs = append(rs, op.Range)
	}
	if e := validateRanges(rs, p.Device.Geometry); e != nil {
		return e
	}
	for _, x := range p.Guards {
		if !x.Range.Valid(p.Device.Geometry) || requireHash(x.SHA256) != nil {
			return errors.New("повреждён guard")
		}
	}
	if p.Mode != "restore" && len(p.Guards) == 0 {
		return errors.New("нет исходных контрольных диапазонов")
	}
	return nil
}

// Analyze reads actual bytes, regardless of previous journal entries. A partial
// range is never silently treated as completed or safe to resume.
func Analyze(ctx context.Context, d Reader, p InstallPlan) ([]string, error) {
	if e := ValidatePlan(p); e != nil {
		return nil, e
	}
	if e := checkDevice(ctx, d, p.Device); e != nil {
		return nil, e
	}
	out := []string{}
	for _, op := range p.Operations {
		h, e := HashRange(ctx, d, op.Range)
		if e != nil {
			return nil, e
		}
		state := "partial-or-changed"
		if h == op.BeforeSHA256 {
			state = "original"
		}
		if h == op.SHA256 {
			state = "written"
		}
		out = append(out, state)
	}
	return out, nil
}

// Execute validates all inputs and original bytes before the first write. It
// journals+syncs each transition and reads back every written byte. It never
// resets or repairs storage. Cancellation is honored between storage operations;
// a write already started must reach readback before returning to the UI.
func Execute(ctx context.Context, d Writer, p InstallPlan, inputs, backup, journal string, approvedHash string, progress Emit, mapper ...string) error {
	if approvedHash != Hash(p) {
		return errors.New("план изменился после просмотра")
	}
	if e := ValidatePlan(p); e != nil {
		return e
	}
	if e := checkDevice(ctx, d, p.Device); e != nil {
		return e
	}
	if !p.BackupWaived {
		if e := VerifyBackup(ctx, backup, p.Device, mapper...); e != nil {
			return e
		}
		h, e := HashFile(filepath.Join(backup, "BackupManifest.json"))
		if e != nil {
			return e
		}
		if h != p.BackupSHA256 {
			return errors.New("бэкап/план изменился")
		}
		if p.Mode == "restore" {
			m, e := LoadBackup(backup)
			if e != nil {
				return e
			}
			if len(m.Extents) != len(p.Operations) {
				return errors.New("restore plan differs from saved physical extents")
			}
			for i, x := range m.Extents {
				op := p.Operations[i]
				if op.Range != x.Range || op.File != x.File || op.SHA256 != x.SHA256 {
					return errors.New("restore plan differs from original backup addresses")
				}
			}
		}
	}
	completed := map[int]bool{}
	if p.PayloadVersion == 1 {
		var e error
		completed, e = completedPayload(journal, approvedHash)
		if e != nil {
			return e
		}
		last := -1
		for i := range completed {
			if i > last {
				last = i
			}
		}
		if last >= len(p.Operations) {
			return errors.New("journal operation outside plan")
		}
		if last >= 0 {
			op := p.Operations[last]
			h, e := HashRange(ctx, d, op.Range)
			if e != nil {
				return e
			}
			if h != op.SHA256 {
				delete(completed, last)
			}
		}
	}
	if e := os.MkdirAll(filepath.Dir(journal), 0700); e != nil {
		return e
	}
	fd, e := syscall.Open(journal, syscall.O_WRONLY|syscall.O_APPEND|syscall.O_CREAT|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	jf := os.NewFile(uintptr(fd), journal)
	defer jf.Close()
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return e
	}
	if e = jf.Chmod(0600); e != nil {
		return e
	}
	sequence := 0
	log := func(i int, phase string) error {
		sequence++
		entry := InstallJournal{Schema, approvedHash, sequence, i, phase, p.BackupWaived}
		return appendJournal(jf, entry)
	}
	if e = log(-1, "preflight-begin"); e != nil {
		return e
	}
	if e = syncDir(filepath.Dir(journal)); e != nil {
		return e
	}
	opened := []*os.File{}
	defer func() {
		for _, f := range opened {
			f.Close()
		}
	}()
	for _, op := range p.Operations {
		path, err := safeFile(inputs, op.File)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		opened = append(opened, f)
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if op.FileOffset > uint64(st.Size()) || op.Range.Length > uint64(st.Size())-op.FileOffset || p.PayloadVersion == 0 && uint64(st.Size()) != op.Range.Length {
			return errors.New("длина входа отличается от плана")
		}
		h, err := hashSection(f, op.FileOffset, op.Range.Length)
		if err != nil {
			return err
		}
		if h != op.SHA256 {
			return errors.New("входной образ повреждён")
		}
	}
	if e = verifyProtected(ctx, d, p.Protected); e != nil {
		return e
	}
	if e = verifyBootFirmwareExecution(ctx, d, p); e != nil {
		return e
	}
	if e = verifyPayloadDescriptor(p, inputs); e != nil {
		return e
	}
	if e = verifyBootFirmwareInputs(p, inputs); e != nil {
		return e
	}
	if p.Recipe == StockRecipe {
		if e = verifyStockExecution(ctx, d, p, inputs); e != nil {
			return e
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = checkDevice(ctx, d, p.Device); e != nil {
		return e
	}
	if e = log(-1, "preflight-complete"); e != nil {
		return e
	}
	meter := newPlanMeter(p, completed, progress)
	if native, ok := d.(*EDLWriter); ok {
		native.progress = meter.emit
		native.completed = completed
		if e = native.authorize(ctx, p, approvedHash); e != nil {
			return e
		}
	}
	commitStarted := false
	defer func() {
		if commitStarted {
			_ = log(-1, "recovery-required")
		}
	}()
	for i, op := range p.Operations {
		meter.current = op.Range.Length
		if !commitStarted {
			if e = ctx.Err(); e != nil {
				return e
			}
		}
		if p.Mode == "restore" && i == 0 || p.BootFirmware != nil && p.BootFirmware.Upgrade && i == len(p.Operations)-3 {
			if e = log(-1, "boot-chain-commit-begin"); e != nil {
				return e
			}
			commitStarted = true
		}
		opCtx := ctx
		if commitStarted {
			opCtx = context.WithoutCancel(ctx)
		}
		if e = checkDevice(opCtx, d, p.Device); e != nil {
			return e
		}
		if _, native := d.(*EDLWriter); !native && p.PayloadVersion == 0 {
			h, err := HashRange(opCtx, d, op.Range)
			if err != nil {
				return err
			}
			if h != op.BeforeSHA256 {
				return errors.New("диапазон изменился перед записью")
			}
		}
		if p.PayloadVersion == 1 && completed[i] {
			if native, ok := d.(*EDLWriter); ok {
				if e = native.Write(opCtx, op.Range, io.NewSectionReader(opened[i], int64(op.FileOffset), int64(op.Range.Length))); e != nil {
					return e
				}
			}
			continue
		}
		if Hash(p) != approvedHash {
			return errors.New("план изменился")
		}
		if e = log(i, "write-begin"); e != nil {
			return e
		}
		emit(meter.emit, "write", uint64(i), uint64(len(p.Operations)), -1, "Запись "+payloadName(op.File))
		// Complete the in-flight write/readback even when the user presses Cancel.
		safeCtx := context.WithoutCancel(ctx)
		if e = d.Write(safeCtx, op.Range, io.NewSectionReader(opened[i], int64(op.FileOffset), int64(op.Range.Length))); e != nil {
			_ = log(i, "write-failed")
			return fmt.Errorf("запись прервана; reset не выполнен: %w", e)
		}
		if e = d.Sync(safeCtx); e != nil {
			return e
		}
		if e = log(i, "write-complete"); e != nil {
			return e
		}
		if e = log(i, "readback-begin"); e != nil {
			return e
		}
		if _, native := d.(*EDLWriter); !native {
			emitBytes(meter.emit, "write", op.Range.Length, op.Range.Length, -1, "Запись "+payloadName(op.File))
			emitBytes(meter.emit, "readback", 0, op.Range.Length, -1, "Подтверждение записи "+payloadName(op.File))
			h, err := HashRange(safeCtx, d, op.Range)
			if err != nil {
				return err
			}
			if h != op.SHA256 {
				_ = log(i, "readback-failed")
				return errors.New("readback не совпал; устройство не перезапущено")
			}
		}
		if e = log(i, "readback-complete"); e != nil {
			return e
		}
		emitBytes(meter.emit, "readback", op.Range.Length, op.Range.Length, -1, "Подтверждение записи "+payloadName(op.File))
		meter.done += 2 * op.Range.Length
	}
	finalCtx := ctx
	if commitStarted {
		finalCtx = context.WithoutCancel(ctx)
	}
	if e = verifyProtected(finalCtx, d, p.Protected); e != nil {
		_ = log(-1, "protected-readback-failed")
		return e
	}
	if e = log(-1, "complete"); e != nil {
		return e
	}
	commitStarted = false
	message := "Установка завершена"
	if p.Mode == "restore" {
		message = "Восстановление завершено"
	}
	emit(meter.emit, "complete", 1, 1, 100, message)
	return nil
}
