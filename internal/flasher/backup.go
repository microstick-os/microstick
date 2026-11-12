package flasher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type BackupRegion struct {
	Range     Range   `json:"range"`
	Mode      string  `json:"mode"`
	Allocated []Range `json:"allocated"`
}
type BackupOptions struct {
	Mapper         string
	AllowFull      bool
	FullPartitions bool // stock transition rollback preserves even unallocated partition bytes
	Emit           Emit
	CheckSpace     func(string, uint64) error
}

// Coverage contains GPT metadata, every partition and both eMMC boot areas. It
// deliberately excludes unpartitioned free gaps: this is not a whole-eMMC dump.
func Coverage(report HardwareReport) ([]Range, error) {
	if e := ValidateInfo(report.Device); e != nil {
		return nil, e
	}
	rs := append([]Range(nil), report.GPT.Metadata...)
	if len(report.GPT.Metadata) != 2 || len(report.GPT.Partitions) == 0 {
		return nil, errors.New("для бэкапа нужна проверенная GPT")
	}
	for _, p := range report.GPT.Partitions {
		rs = append(rs, p.Range)
	}
	for _, area := range []string{"boot1", "boot2"} {
		n := report.Device.Geometry.Areas[area]
		if n == 0 {
			return nil, errors.New("для восстанавливаемого бэкапа нужны обе boot areas")
		}
		rs = append(rs, Range{area, 0, n})
	}
	if e := validateRanges(rs, report.Device.Geometry); e != nil {
		return nil, e
	}
	return sortedRanges(rs), nil
}
func CreateBackup(ctx context.Context, d Reader, report HardwareReport, output string, opt BackupOptions) (BackupManifest, error) {
	m := BackupManifest{Version: Schema, Device: report.Device}
	if e := checkDevice(ctx, d, report.Device); e != nil {
		return m, e
	}
	coverage, e := Coverage(report)
	if e != nil {
		return m, e
	}
	m.Coverage = coverage
	if _, e = os.Lstat(output); !os.IsNotExist(e) {
		return m, errors.New("выберите новый каталог бэкапа")
	}
	if e = os.MkdirAll(filepath.Dir(output), 0700); e != nil {
		return m, e
	}
	stage, e := os.MkdirTemp(filepath.Dir(output), ".backup-")
	if e != nil {
		return m, e
	}
	defer os.RemoveAll(stage)
	working := filepath.Join(stage, ".working")
	if e = os.Mkdir(working, 0700); e != nil {
		return m, e
	}
	staged := map[Range]string{}
	// Determine the actual saved size. Known data filesystems omit free blocks;
	// untrusted maps/journals and raw critical partitions use a full-copy fallback.
	var estimate uint64
	for _, r := range coverage {
		region := BackupRegion{Range: r, Mode: "full", Allocated: []Range{r}}
		isPartition := false
		compactable := false
		for _, p := range report.GPT.Partitions {
			if p.Range == r {
				isPartition = true
				compactable = p.Name == "system" || p.Name == "userdata" || p.Name == "rootfs" || p.Name == "cache"
				break
			}
		}
		if isPartition {
			magic, err := ReadRange(ctx, d, Range{r.Area, r.Offset, min(r.Length, uint64(2048))}, 2048)
			if err != nil {
				return m, err
			}
			if compactable && !opt.FullPartitions && len(magic) >= 1124 && le.Uint16(magic[1080:]) == 0xef53 {
				emitBytes(opt.Emit, "backup-map", 0, r.Length, 15, "Анализ занятых блоков ext4")
				allocated, err := AllocationMap(ctx, d, r, opt.Mapper)
				if err == nil {
					region.Mode = "ext4-allocated"
					region.Allocated = allocated
					m.FreeBlocksOmitted = true
				} else if le.Uint32(magic[1120:])&4 != 0 {
					var path string
					allocated, path, err = journalCompact(ctx, d, r, opt.Mapper, working)
					if err == nil {
						region.Mode = "ext4-journal"
						region.Allocated = allocated
						staged[r] = path
						m.FreeBlocksOmitted = true
					}
				}
				if err != nil {
					if ctx.Err() != nil {
						return m, ctx.Err()
					}
					m.Defects = append(m.Defects, fmt.Sprintf("full-copy fallback at %s/%d: %v", r.Area, r.Offset, err))
				}
			}
			if region.Mode == "full" && !opt.AllowFull {
				return m, fmt.Errorf("требуется полная копия раздела: %d bytes; подтвердите показанный объём", r.Length)
			}
		}
		for _, a := range region.Allocated {
			estimate += a.Length
		}
		m.Regions = append(m.Regions, region)
	}
	check := opt.CheckSpace
	if check == nil {
		check = spaceAvailable
	}
	if e = check(filepath.Dir(output), estimate+(1<<20)); e != nil {
		return m, e
	}
	var done uint64
	lastProgress := uint64(0)
	reportProgress := func(n uint64) {
		if n-lastProgress >= 1<<20 || n == estimate {
			emitBytes(opt.Emit, "backup", n, estimate, 20+int(n*30/max(estimate, 1)), "Сохранение восстанавливаемого бэкапа")
			lastProgress = n
		}
	}
	emitBytes(opt.Emit, "backup", 0, estimate, 20, "Сохранение восстанавливаемого бэкапа")
	for _, region := range m.Regions {
		for _, r := range region.Allocated {
			if e = ctx.Err(); e != nil {
				return m, e
			}
			if e = checkDevice(ctx, d, report.Device); e != nil {
				return m, e
			}
			name := fmt.Sprintf("extent-%06d.bin", len(m.Extents))
			p := filepath.Join(stage, name)
			f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return m, err
			}
			h := sha256.New()
			cw := &countWriter{w: io.MultiWriter(f, h), progress: func(n uint64) { reportProgress(done + n) }}
			if path := staged[region.Range]; path != "" {
				var saved *os.File
				saved, err = os.Open(path)
				if err == nil {
					_, err = io.CopyN(cw, io.NewSectionReader(saved, int64(r.Offset-region.Range.Offset), int64(r.Length)), int64(r.Length))
					saved.Close()
				}
			} else {
				err = d.Read(ctx, r, cw)
			}
			if err == nil && cw.n != r.Length {
				err = io.ErrUnexpectedEOF
			}
			if err == nil {
				err = f.Sync()
			}
			ce := f.Close()
			if err == nil {
				err = ce
			}
			if err != nil {
				return m, err
			}
			m.Extents = append(m.Extents, Extent{r, name, hex.EncodeToString(h.Sum(nil))})
			done += r.Length
			reportProgress(done)
		}
	}
	// The EDL transport owns the offline device for this session. Hash while
	// copying, fsync, then independently verify saved files and both GPTs below.
	// The writer checks target bytes once more immediately before each write.
	if e = checkDevice(ctx, d, report.Device); e != nil {
		return m, e
	}
	m.Complete = true
	if e = os.RemoveAll(working); e != nil {
		return m, e
	}
	if e = SaveJSON(filepath.Join(stage, "BackupManifest.json"), m); e != nil {
		return m, e
	}
	if e = VerifyBackup(ctx, stage, m.Device, opt.Mapper); e != nil {
		return m, e
	}
	if e = os.Rename(stage, output); e != nil {
		return m, e
	}
	return m, syncDir(filepath.Dir(output))
}

type countWriter struct {
	w        io.Writer
	n        uint64
	progress func(uint64)
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, e := c.w.Write(p)
	c.n += uint64(n)
	if c.progress != nil {
		c.progress(c.n)
	}
	return n, e
}
func LoadBackup(root string) (BackupManifest, error) {
	var m BackupManifest
	e := readJSON(filepath.Join(root, "BackupManifest.json"), &m)
	return m, e
}
func verifyBackup(ctx context.Context, root string, device DeviceInfo, mapper ...string) error {
	m, e := LoadBackup(root)
	if e != nil {
		return e
	}
	legacy := m.Device.Identity.Kind == "sahara" && device.Identity.Kind == "storage" && Hash(m.Device.Geometry) == Hash(device.Geometry)
	if m.Version != Schema || !m.Complete || (!EqualDevice(m.Device, device) && !legacy) {
		return errors.New("неполный бэкап или другое устройство")
	}
	if e = validateRanges(m.Coverage, device.Geometry); e != nil {
		return e
	}
	if len(m.Coverage) == 0 || len(m.Regions) != len(m.Coverage) {
		return errors.New("неполное покрытие бэкапа")
	}
	expected := []Range{}
	coverage := []Range{}
	for _, r := range m.Regions {
		coverage = append(coverage, r.Range)
		if r.Mode != "full" && r.Mode != "ext4-allocated" && r.Mode != "ext4-journal" {
			return errors.New("backup region mode")
		}
		if r.Mode == "full" && (len(r.Allocated) != 1 || r.Allocated[0] != r.Range) {
			return errors.New("полный раздел сохранён не целиком")
		}
		if len(r.Allocated) == 0 {
			return errors.New("пустой раздел")
		}
		for _, a := range r.Allocated {
			if a.Area != r.Range.Area || a.Offset < r.Range.Offset || a.Offset-r.Range.Offset > r.Range.Length || a.Length > r.Range.Length-(a.Offset-r.Range.Offset) {
				return errors.New("карта бэкапа вне раздела")
			}
			expected = append(expected, a)
		}
	}
	if Hash(sortedRanges(coverage)) != Hash(sortedRanges(m.Coverage)) {
		return errors.New("backup coverage mismatch")
	}
	if e = validateRanges(expected, device.Geometry); e != nil {
		return e
	}
	actual := []Range{}
	names := map[string]bool{}
	for _, x := range m.Extents {
		if e = ctx.Err(); e != nil {
			return e
		}
		if names[x.File] {
			return errors.New("duplicate backup file")
		}
		names[x.File] = true
		if e = requireHash(x.SHA256); e != nil {
			return e
		}
		p, e := safeFile(root, x.File)
		if e != nil {
			return e
		}
		st, e := os.Stat(p)
		if e != nil {
			return e
		}
		if uint64(st.Size()) != x.Range.Length {
			return errors.New("размер файла бэкапа")
		}
		h, e := HashFile(p)
		if e != nil {
			return e
		}
		if h != x.SHA256 {
			return errors.New("повреждён файл бэкапа")
		}
		actual = append(actual, x.Range)
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		e = f.Sync()
		f.Close()
		if e != nil {
			return e
		}
	}
	if Hash(sortedRanges(actual)) != Hash(sortedRanges(expected)) {
		return errors.New("пропущены диапазоны бэкапа")
	}
	virtual := backupReader{root, m}
	if legacy {
		id, e := backupStorageIdentity(ctx, root, m)
		if e != nil {
			return e
		}
		if id != device.Identity {
			return errors.New("legacy backup storage differs")
		}
	}
	gpt, e := ReadGPT(ctx, virtual, device)
	if e != nil {
		return errors.New("бэкап не содержит обе исходные GPT")
	}
	fullCoverage, e := Coverage(HardwareReport{Device: device, GPT: gpt})
	if e != nil {
		return e
	}
	if Hash(fullCoverage) != Hash(sortedRanges(m.Coverage)) {
		return errors.New("бэкап не покрывает исходные разделы/boot areas")
	}
	for _, region := range m.Regions {
		if region.Mode != "ext4-allocated" && region.Mode != "ext4-journal" {
			continue
		}
		partition := false
		for _, p := range gpt.Partitions {
			if p.Range == region.Range {
				partition = true
			}
		}
		if !partition {
			return errors.New("компактная карта запрещена для GPT/boot areas")
		}
		if len(mapper) != 1 || mapper[0] == "" {
			return errors.New("для проверки компактного бэкапа нужен native mapper")
		}
		if region.Mode == "ext4-journal" {
			if e = verifyJournalRegion(ctx, virtual, region, mapper[0], filepath.Dir(root)); e != nil {
				return e
			}
			continue
		}
		used, e := AllocationMap(ctx, virtual, region.Range, mapper[0])
		if e != nil {
			return errors.New("не подтверждена карта занятости сохранённого ext4")
		}
		// Older compact backups also retained partition slack. Extra saved
		// bytes are valid; missing allocated bytes are never accepted.
		if len(subtractRanges(used, region.Allocated)) != 0 {
			return errors.New("в бэкапе пропущены занятые блоки ext4")
		}
	}
	return syncDir(root)
}

func backupStorageIdentity(ctx context.Context, root string, m BackupManifest) (Identity, error) {
	d := backupReader{root, m}
	gpt, e := ReadGPT(ctx, d, m.Device)
	if e != nil {
		return Identity{}, e
	}
	for _, p := range gpt.Partitions {
		if p.Name == "persist" {
			h, e := HashRange(ctx, d, p.Range)
			if e != nil {
				return Identity{}, e
			}
			return Identity{"storage", fmt.Sprintf("%d:%d:%s", p.Range.Offset, p.Range.Length, h)}, nil
		}
	}
	return Identity{}, errors.New("legacy backup has no persist witness")
}
