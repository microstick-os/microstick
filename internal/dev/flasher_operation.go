package dev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"microstick.dev/tools/internal/flasher"
)

type FlasherOperation struct {
	Registry              *flasher.ProfileReference `json:"registry,omitempty"`
	RegistryProof         []byte                    `json:"registry_proof,omitempty"`
	Measurements          []StageMeasurement        `json:"measurements,omitempty"`
	InstallationID        string                    `json:"installation_uuid,omitempty"`
	SSID                  string                    `json:"ssid,omitempty"`
	ReprepareBootFirmware bool                      `json:"reprepare_boot_firmware,omitempty"`
	Report                flasher.HardwareReport    `json:"report"`
	Release               FlasherRelease            `json:"release"`
	Mode                  string                    `json:"mode"`
	Backup                string                    `json:"backup"`
	Images                string                    `json:"images"`
	Record                string                    `json:"record"`
	Journal               string                    `json:"journal"`
	Prepared              bool                      `json:"prepared"`
	Plan                  *flasher.InstallPlan      `json:"plan,omitempty"`
	Complete              bool                      `json:"complete"`
	Path                  string                    `json:"-"`
}

func FindFlasherOperation(ctx context.Context, p Paths, d flasher.Reader, mode string) (*FlasherOperation, error) {
	paths, e := filepath.Glob(filepath.Join(p.Work, "buildroot-private/flasher-operations/*.json"))
	if e != nil {
		return nil, e
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, path := range paths {
		var op FlasherOperation
		if e = readJSON(path, &op); e != nil {
			return nil, e
		}
		if op.Complete || op.Mode != mode {
			continue
		}
		if native, ok := d.(*flasher.EDLReader); ok {
			if e = native.BindBackup(ctx, op.Report.Device); e != nil {
				continue
			}
		} else {
			info, e := d.Info(ctx)
			if e != nil {
				return nil, e
			}
			if !flasher.EqualDevice(info, op.Report.Device) {
				continue
			}
		}
		for _, s := range []string{op.Backup, op.Images, op.Record, op.Journal} {
			if _, e = ExternalPath(p.Repo, s); e != nil {
				return nil, e
			}
			if !within(s, p.Work) {
				return nil, errors.New("operation path outside workspace")
			}
		}
		op.Path = path
		return &op, nil
	}
	return nil, nil
}
func NewFlasherOperation(p Paths, r flasher.HardwareReport, release FlasherRelease, mode string) *FlasherOperation {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	return &FlasherOperation{Report: r, Release: release, Mode: mode, Backup: filepath.Join(p.Work, "backups/flasher-"+stamp), Images: filepath.Join(p.Work, "artifacts/installations", stamp), Record: filepath.Join(p.Work, "buildroot-private/installations", stamp+".json"), Journal: filepath.Join(p.Work, "buildroot-private/flasher-journals", stamp+".jsonl"), Path: filepath.Join(p.Work, "buildroot-private/flasher-operations", stamp+".json")}
}
func SaveFlasherOperation(op *FlasherOperation) error { return writeJSONAtomic(op.Path, op) }
func RunFlasherOperation(ctx context.Context, p Paths, d flasher.Reader, mapper string, op *FlasherOperation, profile flasher.HardwareProfile, o WizardOptions, progress flasher.Emit) error {
	profile = profile.WithFirmwareVersion(op.Release.Version)
	if c := profile.Compatibility(); c.Status != "" {
		return errors.New(c.Message)
	}
	if profile.ProfileReference() != nil {
		if op.Registry == nil {
			if op.Prepared && !profile.LegacyCompatible() {
				return errors.New("prepared operation lacks its original registry; restore own backup")
			}
			op.Registry = profile.ProfileReference()
			op.RegistryProof = profile.RegistryProof()
		} else if *op.Registry != *profile.ProfileReference() {
			return errors.New("operation requires its original board profile")
		}
	}
	originalProgress := progress
	if progress != nil {
		progress = func(v flasher.Progress) {
			if v.Stage != "write" && v.Stage != "readback" && v.Stage != "complete" {
				v.Overall = -1
			}
			originalProgress(v)
		}
	}
	ctx = flasher.WithBackupCache(ctx)
	if e := os.MkdirAll(filepath.Dir(op.Path), 0700); e != nil {
		return e
	}
	fd, e := syscall.Open(op.Path+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer syscall.Close(fd)
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errors.New("операция уже выполняется")
	}
	if op.Plan != nil && op.Plan.PayloadVersion != 1 {
		return errors.New("незавершённая установка использует прежний формат; восстановите собственный бэкап перед новой установкой")
	}
	if op.Complete {
		return errors.New("операция уже завершена")
	}
	// Old saved operations have no boot ABI attestation. Preserve their evidence
	// and installation record, then prepare again from the original own backup.
	migrated := op.ReprepareBootFirmware
	var preparedInputs flasher.ImageAttestation
	if op.Prepared && op.Plan == nil {
		if e := readJSON(filepath.Join(op.Images, "HardwareInputs.json"), &preparedInputs); e != nil {
			return e
		}
	}
	if op.Report.Device.Identity.Kind != "simulator" && op.Prepared && (op.Plan != nil && op.Plan.BootFirmware == nil || op.Plan == nil && preparedInputs.BootFirmware == nil) {
		if e := writeJSONAtomic(op.Path+".before-boot-firmware", op); e != nil {
			return e
		}
		op.Plan = nil
		migrated = true
		op.ReprepareBootFirmware = true
	}
	// A saved valid plan may contain partially written TZ/HYP. Resume uses actual
	// raw bytes and its original attestation, without requiring a bootable DTB.
	if op.Report.Device.Identity.Kind != "simulator" && op.Plan == nil && !migrated {
		firmware, e := flasher.InspectBootFirmware(ctx, d, op.Report, profile.BootFirmwarePolicy)
		if e != nil {
			return e
		}
		op.Report.BootFirmware = firmware
	}
	if op.InstallationID == "" {
		op.InstallationID = o.InstallationID
		if op.InstallationID == "" {
			op.InstallationID, e = NewInstallationUUID()
			if e != nil {
				return e
			}
		}
	}
	o.InstallationID = op.InstallationID
	if op.SSID == "" {
		op.SSID = o.SSID
	}
	o.SSID = op.SSID
	o.Emit = progress
	if e = SaveFlasherOperation(op); e != nil {
		return e
	}
	mark := measureStage(d, "backup")
	if _, e = os.Stat(op.Backup); os.IsNotExist(e) {
		_, e = flasher.CreateBackup(ctx, d, op.Report, op.Backup, flasher.BackupOptions{Mapper: mapper, AllowFull: true, Emit: progress})
		if e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	if e = flasher.VerifyBackup(ctx, op.Backup, op.Report.Device, mapper); e != nil {
		return e
	}
	op.Measurements = append(op.Measurements, mark())
	snapshot, e := flasher.OpenSnapshot(d, op.Backup)
	if e != nil {
		return e
	}
	if migrated {
		images, firmware, e := upgradePreparedBootFirmware(ctx, p, snapshot, op.Report, profile, op.Record, op.Images)
		if e != nil {
			return e
		}
		op.Images, op.Report.BootFirmware = images, firmware
		if e = SaveFlasherOperation(op); e != nil {
			return e
		}
	}
	o.Update = op.Mode == "update"
	o.Release = &op.Release
	if profile.Recipe == flasher.StockRecipe {
		storage, e := flasher.StockStorage(op.Report, o.Clean, true)
		if e != nil {
			return e
		}
		o.Storage = &storage
	}
	for _, part := range op.Report.GPT.Partitions {
		if part.Name == "rootfs" || part.Name == "system" {
			o.RootPARTUUID = part.GUID
			o.RootCapacity = part.Range.Length
		}
	}
	mark = measureStage(d, "prepare")
	if !op.Prepared {
		// A cancelled preparation may have atomically published images before its
		// attestation/state checkpoint. Keep that attempt and stage a fresh output
		// with the same installation record instead of asking for a new record.
		if _, e = os.Lstat(op.Images); e == nil {
			op.Images += ".retry-" + time.Now().UTC().Format("20060102T150405.000000000Z")
			if e = SaveFlasherOperation(op); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if progress != nil {
			progress(flasher.Progress{Stage: "prepare", Message: "Подготовка системы"})
		}
		if op.Report.Device.Identity.Kind == "simulator" && profile.Recipe == "simulator-v1" {
			if !o.Clean {
				temp, e := os.MkdirTemp(filepath.Join(p.Work, "buildroot-private"), ".simulation-source-")
				if e != nil {
					return e
				}
				defer os.RemoveAll(temp)
				for _, part := range op.Report.GPT.Partitions {
					if part.Name == "rootfs" {
						o.OldRootfs = filepath.Join(temp, "rootfs.ext4")
						f, e := os.OpenFile(o.OldRootfs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
						if e != nil {
							return e
						}
						e = snapshot.Read(ctx, part.Range, f)
						ce := f.Close()
						if e != nil {
							return e
						}
						if ce != nil {
							return ce
						}
					}
				}
			}
			if e = PrepareWizard(p, p.Artifacts, op.Record, op.Images, o); e != nil {
				return e
			}
		} else if _, e = PrepareDeviceWizard(ctx, p, snapshot, op.Report, profile, op.Record, op.Images, o); e != nil {
			return e
		}
		op.Prepared = true
		if e = SaveFlasherOperation(op); e != nil {
			return e
		}
	}
	op.Measurements = append(op.Measurements, mark())
	mark = measureStage(d, "plan")
	if op.Plan == nil {
		var plan flasher.InstallPlan
		var source flasher.Reader = d
		if migrated {
			source = snapshot
		}
		if profile.Recipe == "simulator-v1" {
			plan, e = flasher.BuildInstallPlan(ctx, d, op.Report, op.Images, "", op.Backup, mapper)
		} else {
			plan, e = flasher.BuildQualifiedInstallPlan(ctx, source, op.Report, profile, op.Images, "", op.Backup, mapper)
		}
		if e != nil {
			return e
		}
		if migrated {
			plan, e = flasher.ResumePlan(ctx, d, plan, profile)
			if e != nil {
				return e
			}
		}
		plan.Mode = op.Mode
		op.Plan = &plan
		op.ReprepareBootFirmware = false
	} else {
		plan, e := flasher.ResumePlan(ctx, d, *op.Plan, profile)
		if e != nil {
			return e
		}
		op.Plan = &plan
	}
	if e = SaveFlasherOperation(op); e != nil {
		return e
	}
	op.Measurements = append(op.Measurements, mark())
	writer, e := FlasherWriter(d, *op.Plan)
	if e != nil {
		return e
	}
	mark = measureStage(d, "write-and-confirm")
	if e = flasher.Execute(ctx, writer, *op.Plan, op.Images, op.Backup, op.Journal, flasher.Hash(*op.Plan), progress, mapper); e != nil {
		return e
	}
	op.Measurements = append(op.Measurements, mark())
	op.Complete = true
	return SaveFlasherOperation(op)
}
func FlasherWriter(d flasher.Reader, p flasher.InstallPlan) (flasher.Writer, error) {
	if native, ok := d.(*flasher.EDLReader); ok {
		return flasher.NewEDLWriter(native, p, flasher.Hash(p))
	}
	if w, ok := d.(flasher.Writer); ok {
		return w, nil
	}
	return nil, fmt.Errorf("устройство не поддерживает запись")
}

func FlasherBackups(ctx context.Context, p Paths, d flasher.Reader) ([]string, error) {
	paths, e := filepath.Glob(filepath.Join(p.Work, "backups/*/BackupManifest.json"))
	if e != nil {
		return nil, e
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	out := []string{}
	for _, path := range paths {
		m, e := flasher.LoadBackup(filepath.Dir(path))
		if e != nil || !m.Complete {
			continue
		}
		if native, ok := d.(*flasher.EDLReader); ok {
			if e = native.BindOwnBackup(ctx, filepath.Dir(path), m); e != nil {
				continue
			}
		} else {
			info, e := d.Info(ctx)
			if e != nil {
				return nil, e
			}
			if !flasher.EqualDevice(info, m.Device) {
				continue
			}
		}
		out = append(out, filepath.Dir(path))
	}
	return out, nil
}

// OperationProfile reopens the original authenticated snapshot for a retry.
// The current registry supplies revocations, without replacing prepared inputs.
func OperationProfile(op *FlasherOperation, current *flasher.RegistrySnapshot, version string) (flasher.HardwareProfile, error) {
	if op.Registry != nil {
		return current.PinnedProfile(op.RegistryProof, *op.Registry, version, "")
	}
	for _, p := range current.Profiles(version, "", nil) {
		if p.ID == op.Report.Resolution.Profile {
			return p, nil
		}
	}
	return flasher.HardwareProfile{}, flasher.ErrUnsupported
}
