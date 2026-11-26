// Package flasher owns compatibility and storage policy. User interfaces only
// display its results. Hardware access is deliberately separate from simulation.
package flasher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync/atomic"
	"syscall"
)

const Schema = 1
const Chunk = 16384

var ErrUnsupported = errors.New("нет подходящего профиля платы в каталоге")

// A recognized board can have damaged/missing inputs. Keep its specific cause
// without mislabelling it as an unknown board or exposing native diagnostics.
type UnsupportedInput struct{ Code, Message string }

func (e *UnsupportedInput) Error() string { return e.Message }
func (e *UnsupportedInput) Unwrap() error { return ErrUnsupported }

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Identity struct {
	Kind     string `json:"kind"` // sahara or simulator; USB serial is never an identity
	Hardware string `json:"hardware"`
}
type Geometry struct {
	SectorSize uint64            `json:"sector_size"`
	Areas      map[string]uint64 `json:"areas_bytes"`
}
type DeviceInfo struct {
	Identity Identity `json:"identity"`
	Geometry Geometry `json:"geometry"`
}
type Range struct {
	Area   string `json:"area"`
	Offset uint64 `json:"offset"`
	Length uint64 `json:"length"`
}

func (r Range) Valid(g Geometry) bool {
	n, ok := g.Areas[r.Area]
	return ok && g.SectorSize == 512 && r.Length > 0 && r.Offset <= n && r.Length <= n-r.Offset && r.Offset%512 == 0 && r.Length%512 == 0
}

// Reader exposes no write, reset, erase, provisioning or register operations.
type Reader interface {
	Info(context.Context) (DeviceInfo, error)
	Read(context.Context, Range, io.Writer) error
}
type Writer interface {
	Reader
	Write(context.Context, Range, io.Reader) error
	Sync(context.Context) error
}

func EqualDevice(a, b DeviceInfo) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func ValidateInfo(d DeviceInfo) error {
	if (d.Identity.Hardware == "" && d.Identity.Kind != "firehose") || (d.Identity.Kind != "sahara" && d.Identity.Kind != "storage" && d.Identity.Kind != "firehose" && d.Identity.Kind != "simulator") || d.Geometry.SectorSize != 512 || d.Geometry.Areas["user"] < 68*512 {
		return errors.New("неполная аппаратная идентичность/геометрия")
	}
	for area, n := range d.Geometry.Areas {
		if area != "user" && area != "boot1" && area != "boot2" || n == 0 || n%512 != 0 || n > 1<<44 {
			return errors.New("неподдерживаемая физическая область")
		}
	}
	return nil
}

type Partition struct {
	Name     string `json:"name"`
	TypeGUID string `json:"type_guid"`
	GUID     string `json:"guid"`
	Range    Range  `json:"range"`
}
type PartitionTable struct {
	DiskGUID   string      `json:"disk_guid"`
	Partitions []Partition `json:"partitions"`
	Metadata   []Range     `json:"metadata"`
}
type Resource struct {
	Node       string   `json:"node"`
	Property   string   `json:"property"`
	Controller string   `json:"controller"`
	Cells      []uint32 `json:"cells,omitempty"`
	State      string   `json:"state,omitempty"`
	Enabled    bool     `json:"enabled"`
}
type DTVariant struct {
	Source              string     `json:"source"`
	Offset              uint64     `json:"offset"`
	Bytes               uint64     `json:"bytes"`
	SHA256              string     `json:"sha256"`
	BoardID             []uint32   `json:"board_id,omitempty"`
	Compatible          []string   `json:"compatible,omitempty"`
	Resources           []Resource `json:"resources"`
	Issues              []string   `json:"issues,omitempty"`
	MapSHA256           string     `json:"map_sha256"`
	CompatibilitySHA256 string     `json:"compatibility_sha256,omitempty"`
	Selectors           [][]uint32 `json:"container_selectors,omitempty"`
}
type HardwareReport struct {
	ActiveOrigin  string         `json:"active_origin,omitempty"`
	ActiveDTB     []byte         `json:"active_dtb,omitempty"`
	BootFirmware  *BootFirmware  `json:"boot_firmware,omitempty"`
	Version       int            `json:"schema_version"`
	Device        DeviceInfo     `json:"device"`
	GPT           PartitionTable `json:"gpt"`
	Variants      []DTVariant    `json:"dt_variants"`
	AppliedSHA256 string         `json:"applied_dtb_sha256,omitempty"`
	Issues        []string       `json:"issues"`
	Resolution    Resolution     `json:"resolution"`
	Boot          BootProvenance `json:"boot"`
	InputFailure  string         `json:"input_failure,omitempty"`
}
type Evidence struct {
	URL    string `json:"url"`
	Commit string `json:"commit,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Bytes  uint64 `json:"bytes,omitempty"`
	Scope  string `json:"scope"`
}
type HardwareProfile struct {
	automatic          *automaticProfile
	authorization      *profileAuthorization
	BootFirmwarePolicy string              `json:"boot_firmware_policy,omitempty"`
	LayoutSHA256       string              `json:"layout_sha256,omitempty"`
	Version            int                 `json:"schema_version"`
	ID                 string              `json:"id"`
	DTBHashes          []string            `json:"dtb_sha256"`
	MapHashes          []string            `json:"map_sha256"`
	RequiredAreas      []string            `json:"required_areas"`
	KernelBuild        int                 `json:"kernel_build"`
	Bundle             map[string]string   `json:"bundle_sha256"`
	FirmwareSource     string              `json:"firmware_source,omitempty"`
	Recipe             string              `json:"recipe"`
	Evidence           []Evidence          `json:"evidence"`
	AllowedDifferences []string            `json:"allowed_differences,omitempty"`
	Board              string              `json:"physical_board,omitempty"`
	Layout             string              `json:"required_layout,omitempty"`
	SourceSets         []ReviewedSourceSet `json:"reviewed_source_sets,omitempty"`
}

// A source set is an exact reviewed container, not an equivalence assertion.
// Issues remain visible; only this precise set can use its documented review.
type ReviewedSourceSet struct {
	ID                       string `json:"id"`
	Fingerprint              string `json:"fingerprint"`
	CompatibilityFingerprint string `json:"compatibility_fingerprint,omitempty"`
	Review                   string `json:"review"`
}
type Resolution struct {
	Status     string   `json:"status"`
	Profile    string   `json:"profile,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Reasons    []string `json:"reasons"`
}
type Extent struct {
	Range  Range  `json:"range"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type BackupManifest struct {
	Version           int            `json:"schema_version"`
	Device            DeviceInfo     `json:"device"`
	Coverage          []Range        `json:"coverage"`
	Regions           []BackupRegion `json:"regions"`
	Extents           []Extent       `json:"extents"`
	Defects           []string       `json:"defects,omitempty"`
	Complete          bool           `json:"complete"`
	FreeBlocksOmitted bool           `json:"free_blocks_omitted"`
}
type Operation struct {
	FileOffset   uint64 `json:"file_offset,omitempty"`
	Range        Range  `json:"range"`
	File         string `json:"file"`
	SHA256       string `json:"sha256"`
	BeforeSHA256 string `json:"before_sha256"`
}
type InstallPlan struct {
	Registry           *ProfileReference `json:"registry,omitempty"`
	PayloadSHA256      string            `json:"payload_sha256,omitempty"`
	PayloadVersion     int               `json:"payload_version,omitempty"`
	Targets            []Partition       `json:"targets,omitempty"`
	BootFirmware       *BootFirmware     `json:"boot_firmware,omitempty"`
	catalogProfile     *HardwareProfile
	Version            int            `json:"schema_version"`
	Device             DeviceInfo     `json:"device"`
	Profile            string         `json:"profile"`
	Recipe             string         `json:"recipe"`
	Mode               string         `json:"mode"`
	BackupSHA256       string         `json:"backup_manifest_sha256,omitempty"`
	BackupWaived       bool           `json:"backup_waived"`
	Guards             []Extent       `json:"guards"`
	Operations         []Operation    `json:"operations"`
	Storage            *StorageRecipe `json:"storage,omitempty"`
	InstallationSHA256 string         `json:"installation_receipt_sha256,omitempty"`
	Protected          []Extent       `json:"protected_after,omitempty"`
}
type InstallJournal struct {
	Version      int    `json:"schema_version"`
	PlanSHA256   string `json:"plan_sha256"`
	Sequence     int    `json:"sequence"`
	Operation    int    `json:"operation"`
	Phase        string `json:"phase"`
	BackupWaived bool   `json:"backup_waived"`
}
type Progress struct {
	// Plan counters include each payload byte once for writing and once for
	// readback. Local range counters retain their existing meaning for details.
	PlanDoneBytes  uint64 `json:"plan_done_bytes,omitempty"`
	PlanTotalBytes uint64 `json:"plan_total_bytes,omitempty"`
	Unit           string `json:"unit,omitempty"`
	Version        int    `json:"schema_version"`
	Stage          string `json:"stage"`
	Done           uint64 `json:"done"`
	Total          uint64 `json:"total"`
	Overall        int    `json:"overall_percent"`
	Message        string `json:"message"`
}
type Emit func(Progress)

func emit(f Emit, s string, done, total uint64, overall int, msg string) {
	if f != nil {
		f(Progress{Version: Schema, Stage: s, Done: done, Total: total, Overall: overall, Message: msg})
	}
}
func Hash(v any) string         { b, _ := json.Marshal(v); return HashBytes(b) }
func HashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

var localHashBytes atomic.Uint64

func LocalHashBytes() uint64 { return localHashBytes.Load() }
func HashFile(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	localHashBytes.Add(uint64(n))
	return hex.EncodeToString(h.Sum(nil)), e
}
func HashRange(ctx context.Context, d Reader, r Range) (string, error) {
	h := sha256.New()
	e := d.Read(ctx, r, h)
	return hex.EncodeToString(h.Sum(nil)), e
}
func ReadRange(ctx context.Context, d Reader, r Range, max uint64) ([]byte, error) {
	if r.Length > max {
		return nil, errors.New("диапазон превышает предел разбора")
	}
	b := &boundedBuffer{left: r.Length}
	e := d.Read(ctx, r, b)
	if e == nil && b.left != 0 {
		e = io.ErrUnexpectedEOF
	}
	return b.data, e
}

type boundedBuffer struct {
	data []byte
	left uint64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if uint64(len(p)) > b.left {
		return 0, io.ErrShortBuffer
	}
	b.data = append(b.data, p...)
	b.left -= uint64(len(p))
	return len(p), nil
}
func readJSON(p string, v any) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 16<<20))
	dec.DisallowUnknownFields()
	if e = dec.Decode(v); e != nil {
		return e
	}
	var extra any
	if e = dec.Decode(&extra); e != io.EOF {
		return errors.New("лишние данные JSON")
	}
	return nil
}
func SaveJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicFile(p, append(b, '\n'))
}
func LoadReport(p string) (HardwareReport, error) {
	var v HardwareReport
	e := readJSON(p, &v)
	if e == nil && v.Version != Schema {
		e = errors.New("версия отчёта")
	}
	return v, e
}
func syncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func atomicFile(p string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".stage-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), p); e != nil {
		return e
	}
	return syncDir(filepath.Dir(p))
}
func safeFile(root, name string) (string, error) {
	if !regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`).MatchString(name) || name == "." || name == ".." {
		return "", errors.New("небезопасное имя артефакта")
	}
	p := filepath.Join(root, name)
	st, e := os.Lstat(p)
	if e != nil {
		return "", e
	}
	if !st.Mode().IsRegular() {
		return "", errors.New("артефакт должен быть обычным файлом")
	}
	return p, nil
}
func sortedRanges(rs []Range) []Range {
	out := append([]Range(nil), rs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Area != out[j].Area {
			return out[i].Area < out[j].Area
		}
		return out[i].Offset < out[j].Offset
	})
	return out
}
func validateRanges(rs []Range, g Geometry) error {
	rs = sortedRanges(rs)
	for i, r := range rs {
		if !r.Valid(g) {
			return errors.New("диапазон вне физической геометрии")
		}
		if i > 0 && rs[i-1].Area == r.Area && rs[i-1].Offset+rs[i-1].Length > r.Offset {
			return errors.New("пересечение физических диапазонов")
		}
	}
	return nil
}
func spaceAvailable(p string, n uint64) error {
	var s syscall.Statfs_t
	if e := syscall.Statfs(p, &s); e != nil {
		return e
	}
	if uint64(s.Bavail)*uint64(s.Bsize) < n {
		return errors.New("недостаточно свободного места")
	}
	return nil
}
func checkDevice(ctx context.Context, d Reader, expected DeviceInfo) error {
	actual, e := d.Info(ctx)
	if e != nil {
		return e
	}
	if !EqualDevice(actual, expected) {
		return errors.New("сменилось устройство или геометрия")
	}
	return nil
}
func requireHash(s string) error {
	if !digestPattern.MatchString(s) {
		return fmt.Errorf("некорректный SHA256")
	}
	return nil
}

func emitBytes(f Emit, s string, done, total uint64, overall int, msg string) {
	if f != nil {
		f(Progress{Version: Schema, Stage: s, Done: done, Total: total, Overall: overall, Message: msg, Unit: "bytes"})
	}
}
