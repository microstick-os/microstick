package flasher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// EDLReader keeps one native USB session. It deliberately does not implement
// Writer. Closing/cancelling it never calls a target reset or reconnect.
type EDLReader struct {
	readBytes, writeBytes uint64
	mu                    sync.Mutex
	cmd                   *exec.Cmd
	in                    io.WriteCloser
	out                   *bufio.Reader
	identity              Identity
	chip                  Identity
	geometry              Geometry
	Diagnostic            string
	log                   *os.File
	closed                bool
	Protocol              string
}

// SecureBoot reads the four authentication enable bits from the supported SoC.
// It never exposes a caller-controlled memory address or raw fuse contents.
func (d *EDLReader) SecureBoot(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if d.closed {
		return "", io.ErrClosedPipe
	}
	if _, e := io.WriteString(d.in, "S\n"); e != nil {
		return "", e
	}
	line, e := d.out.ReadString('\n')
	if e != nil {
		return "", e
	}
	switch line {
	case "S enabled\n":
		return "enabled", nil
	case "S disabled\n":
		return "disabled", nil
	}
	return "", errors.New("Аппаратная проверка Secure Boot недоступна; загрузочная firmware не изменялась")
}

func OpenEDL(ctx context.Context, helper, programmer, serial string) (*EDLReader, error) {
	c := exec.CommandContext(ctx, helper, programmer, serial)
	in, e := c.StdinPipe()
	if e != nil {
		return nil, e
	}
	out, e := c.StdoutPipe()
	if e != nil {
		return nil, e
	}
	log, e := os.CreateTemp(filepath.Dir(helper), "edl-diagnostic-*.log")
	if e != nil {
		return nil, e
	}
	if e = log.Chmod(0600); e != nil {
		log.Close()
		return nil, e
	}
	c.Stderr = log
	if e = c.Start(); e != nil {
		log.Close()
		return nil, e
	}
	d := &EDLReader{cmd: c, in: in, out: bufio.NewReader(out), log: log, Diagnostic: log.Name()}
	info, e := d.readInfo()
	if e != nil {
		d.Close()
		return nil, fmt.Errorf("%w. Запись не начата. Приватная диагностика: %s", e, d.Diagnostic)
	}
	d.identity = info.Identity
	if info.Identity.Kind == "sahara" {
		d.chip = info.Identity
	}
	d.geometry = info.Geometry
	return d, nil
}
func (d *EDLReader) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	d.in.Close()
	if d.cmd.Process != nil {
		d.cmd.Process.Kill()
	}
	_ = d.cmd.Wait()
	if d.log != nil {
		d.log.Close()
	}
	return nil
}
func (d *EDLReader) readInfo() (DeviceInfo, error) {
	line, e := d.out.ReadString('\n')
	if e != nil {
		return DeviceInfo{}, fmt.Errorf("%w: native helper завершился до передачи геометрии. Устройство не перезапущено", ErrEDLTransport)
	}
	if len(line) > 256 {
		return DeviceInfo{}, errors.New("EDL identity protocol")
	}
	p := strings.Fields(line)
	if len(p) > 0 && p[0] == "E" {
		if len(p) != 2 {
			return DeviceInfo{}, edlStageError("")
		}
		return DeviceInfo{}, edlStageError(p[1])
	}
	if len(p) == 7 && p[0] == "I2" {
		if p[1] != "sahara" && p[1] != "firehose" {
			return DeviceInfo{}, errors.New("EDL session protocol")
		}
		d.Protocol = p[1]
		p = append([]string{"I"}, p[2:]...)
	}
	if len(p) != 6 || p[0] != "I" || len(p[1]) != 8 || len(p[2]) != 16 {
		return DeviceInfo{}, errors.New("EDL identity protocol")
	}
	a, e := strconv.ParseUint(p[1], 16, 32)
	if e != nil || (a == 0 && d.Protocol != "firehose") {
		return DeviceInfo{}, errors.New("EDL Sahara serial missing")
	}
	b, e := strconv.ParseUint(p[2], 16, 64)
	if e != nil || (b == 0 && d.Protocol != "firehose") {
		return DeviceInfo{}, errors.New("EDL hardware ID missing")
	}
	info := DeviceInfo{Identity{"sahara", p[1] + ":" + p[2]}, Geometry{512, map[string]uint64{}}}
	if d.Protocol == "firehose" {
		info.Identity = Identity{Kind: "firehose"}
	}
	for i, area := range []string{"user", "boot1", "boot2"} {
		n, e := strconv.ParseUint(p[3+i], 10, 64)
		if e != nil {
			return info, e
		}
		info.Geometry.Areas[area] = n
	}
	return info, ValidateInfo(info)
}

var ErrEDLTransport = errors.New("EDL transport interrupted")

type edlFailure struct{ stage, message string }

func (e *edlFailure) Error() string { return e.message }
func RetryableEDL(err error) bool {
	if errors.Is(err, ErrEDLTransport) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var failure *edlFailure
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.stage {
	case "device-not-found", "session-detect", "sahara-identity", "programmer-upload", "source-read", "write-transfer", "readback-read":
		return true
	}
	return false
}
func edlStageError(stage string) error {
	message := "неизвестный этап отказа native helper"
	switch stage {
	case "USB_PERMISSION_DENIED":
		message = "USB_PERMISSION_DENIED: libusb ACCESS/-3; нет доступа rw к USB-node 05c6:9008. Проверьте текущие Bus/Device и выдайте доступ пользователю. ACL/udev автоматически не изменяются"
	case "device-not-found":
		message = "устройство 05c6:9008 не найдено, недоступно для USB-чтения или не совпало с выбранным USB serial. Проверьте EDL-режим, права доступа и подключение"
	case "multiple-devices":
		message = "найдено несколько устройств 05c6:9008; укажите --edl-serial"
	case "usb-init":
		message = "не удалось инициализировать USB backend"
	case "usb-open":
		message = "не удалось открыть USB-устройство. Проверьте права доступа и не занят ли интерфейс другой программой"
	case "sahara-identity":
		message = "не прошёл обмен идентификацией Sahara"
	case "session-detect":
		message = "устройство не ответило корректным Sahara HELLO или Firehose ACK"
	case "source-read":
		message = "не удалось прочитать исходные байты перед записью"
	case "source-changed":
		message = "исходный диапазон изменился перед записью"
	case "write-transfer":
		message = "передача записываемого образа прервалась; доступен откат из бэкапа"
	case "input-hash":
		message = "входной образ изменился во время передачи; доступен откат из бэкапа"
	case "readback-read":
		message = "не удалось перечитать записанный диапазон; доступен откат из бэкапа"
	case "readback-hash":
		message = "прочитанные байты не совпали с образом; доступен откат из бэкапа"
	case "programmer-file":
		message = "не удалось загрузить локальный файл EDL programmer"
	case "programmer-upload":
		message = "не прошла загрузка programmer в RAM через Sahara"
	case "firehose-configure":
		message = "не прошла начальная настройка протокола Firehose"
	case "geometry-user":
		message = "Firehose не сообщил поддерживаемую геометрию основной области eMMC (user)"
	case "geometry-boot1":
		message = "Firehose не сообщил поддерживаемую геометрию области eMMC boot1"
	case "geometry-boot2":
		message = "Firehose не сообщил поддерживаемую геометрию области eMMC boot2"
	}
	return &edlFailure{stage, "EDL: " + message + ". Сессия остановлена; reset не выполнен"}
}
func (d *EDLReader) Info(ctx context.Context) (DeviceInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return DeviceInfo{}, e
	}
	if _, e := io.WriteString(d.in, "I\n"); e != nil {
		return DeviceInfo{}, e
	}
	info, e := d.readInfo()
	if e != nil {
		return info, e
	}
	if Hash(info.Geometry) != Hash(d.geometry) {
		return DeviceInfo{}, errors.New("EDL device changed")
	}
	if d.identity.Kind == "storage" {
		info.Identity = d.identity
	}
	return info, nil
}
func (d *EDLReader) Read(ctx context.Context, r Range, w io.Writer) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !r.Valid(d.geometry) {
		return errors.New("EDL read bounds")
	}
	lun := map[string]int{"user": 0, "boot1": 1, "boot2": 2}[r.Area]
	for pos := uint64(0); pos < r.Length; {
		if e := ctx.Err(); e != nil {
			return e
		}
		n := min(uint64(1<<20), r.Length-pos)
		if _, e := fmt.Fprintf(d.in, "R %d %d %d\n", lun, r.Offset+pos, n); e != nil {
			return e
		}
		if _, e := io.CopyN(w, d.out, int64(n)); e != nil {
			return fmt.Errorf("%w: read interrupted; reset suppressed", ErrEDLTransport)
		}
		d.readBytes += n
		pos += n
	}
	return nil
}

// EDLWriter is created only for the confirmed execution phase. The shared native
// process remains read-only until Execute finishes every preflight and grants
// the exact plan, including source and expected payload hashes.
type EDLWriter struct {
	*EDLReader
	plan      InstallPlan
	approved  string
	next      int
	armed     bool
	progress  Emit
	completed map[int]bool
}

func NewEDLWriter(d *EDLReader, p InstallPlan, approved string) (*EDLWriter, error) {
	if Hash(p) != approved || !nativeIdentity(p.Device.Identity) {
		return nil, errors.New("native write plan mismatch")
	}
	if e := ValidatePlan(p); e != nil {
		return nil, e
	}
	if p.Mode != "restore" && (p.qualification == nil || p.qualification.ID != p.Profile || !p.qualification.Qualified || p.qualification.Recipe != p.Recipe) {
		return nil, ErrUnsupported
	}
	// Keep a private plan copy: shared slice mutation must not alter the grant.
	p.Operations = append([]Operation(nil), p.Operations...)
	p.Guards = append([]Extent(nil), p.Guards...)
	return &EDLWriter{EDLReader: d, plan: p, approved: approved}, nil
}
func (d *EDLWriter) authorize(ctx context.Context, p InstallPlan, approved string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if d.armed || approved != d.approved || Hash(p) != approved || Hash(d.plan) != approved {
		return errors.New("native plan grant differs")
	}
	if _, e := fmt.Fprintf(d.in, "%s %s %d\n", func() string {
		if p.PayloadVersion == 1 {
			return "A2"
		}
		return "A"
	}(), approved, len(p.Operations)); e != nil {
		return e
	}
	for index, op := range p.Operations {
		before := op.BeforeSHA256
		if p.PayloadVersion == 1 {
			before = "-"
			if d.completed[index] {
				before = op.SHA256
			}
		}
		lun := map[string]int{"user": 0, "boot1": 1, "boot2": 2}[op.Range.Area]
		if _, e := fmt.Fprintf(d.in, "%d %d %d %s %s\n", lun, op.Range.Offset, op.Range.Length, before, op.SHA256); e != nil {
			return e
		}
	}
	line, e := d.out.ReadString('\n')
	if e != nil || line != "ARMED\n" {
		return errors.New("native bridge rejected exact range grant")
	}
	d.armed = true
	return nil
}
func (d *EDLWriter) Write(ctx context.Context, r Range, src io.Reader) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if !d.armed || d.next >= len(d.plan.Operations) || r != d.plan.Operations[d.next].Range {
		return errors.New("native write outside approved plan")
	}
	if _, e := fmt.Fprintf(d.in, "W %s %d\n", d.approved, d.next); e != nil {
		return e
	}
	line, e := d.writeResponse(r)
	if e != nil {
		return e
	}
	if e == nil && strings.HasPrefix(line, "VERIFIED ") {
		if e = d.receipt(line, "VERIFIED", r); e != nil {
			return e
		}
		d.next++
		return nil
	}
	if e != nil || line != "READY\n" {
		return errors.New("native source bytes/geometry changed before write")
	}
	counted := &writeProgressReader{Reader: src, total: r.Length, progress: func(p Progress) {
		p.Message = "Запись " + payloadName(d.plan.Operations[d.next].File)
		if d.progress != nil {
			d.progress(p)
		}
	}}
	sent, copyErr := io.CopyN(d.in, counted, int64(r.Length))
	d.writeBytes += uint64(sent)
	if e = copyErr; e != nil {
		return e
	}
	line, e = d.writeResponse(r)
	if e != nil {
		return e
	}
	if e != nil || d.receipt(line, "DONE", r) != nil {
		return errors.New("native write/readback interrupted; no reset")
	}
	d.next++
	return nil
}

func (d *EDLWriter) receipt(line, kind string, r Range) error {
	p := strings.Fields(line)
	if len(p) != 3 || p[0] != kind {
		return errors.New("native readback receipt")
	}
	n, e := strconv.ParseUint(p[1], 10, 64)
	if e != nil || n != r.Length || p[2] != d.plan.Operations[d.next].SHA256 {
		return errors.New("native readback digest differs")
	}
	return nil
}
func (d *EDLWriter) writeResponse(r Range) (string, error) {
	var previous uint64
	for {
		line, e := d.out.ReadString('\n')
		if e != nil {
			return "", fmt.Errorf("%w: native response", ErrEDLTransport)
		}
		if len(line) > 256 {
			return "", errors.New("native progress bounds")
		}
		if strings.HasPrefix(line, "E ") {
			p := strings.Fields(line)
			if len(p) != 2 {
				return "", edlStageError("")
			}
			return "", edlStageError(p[1])
		}
		if !strings.HasPrefix(line, "P ") {
			return line, nil
		}
		p := strings.Fields(line)
		if len(p) != 4 || (p[1] != "source" && p[1] != "readback") {
			return "", errors.New("native progress protocol")
		}
		n, e := strconv.ParseUint(p[2], 10, 64)
		if e != nil {
			return "", e
		}
		total, e := strconv.ParseUint(p[3], 10, 64)
		if e != nil || total != r.Length || n > total {
			return "", errors.New("native progress length")
		}
		d.readBytes += n - previous
		previous = n
		emitBytes(d.progress, p[1], n, total, -1, "Подтверждение записи "+payloadName(d.plan.Operations[d.next].File))
	}
}

type writeProgressReader struct {
	io.Reader
	done, total uint64
	progress    Emit
	last        uint64
}

func (r *writeProgressReader) Read(b []byte) (int, error) {
	n, e := r.Reader.Read(b)
	r.done += uint64(n)
	if r.done-r.last >= 1<<20 || r.done == r.total {
		emitBytes(r.progress, "write", r.done, r.total, 65, "Запись образа")
		r.last = r.done
	}
	return n, e
}
func (d *EDLWriter) Sync(ctx context.Context) error { return ctx.Err() } // program ACK plus native readback completed before DONE

func nativeIdentity(id Identity) bool {
	return id.Hardware != "" && (id.Kind == "sahara" || id.Kind == "storage")
}

// BindStorage is honest storage evidence, not a synthetic chip serial. persist
// is untouched by installation; its address is retained for damaged-GPT restore.
func (d *EDLReader) BindStorage(ctx context.Context, partitions []Partition) error {
	for _, p := range partitions {
		if p.Name == "persist" {
			h, e := HashRange(ctx, d, p.Range)
			if e != nil {
				return e
			}
			d.identity = Identity{"storage", fmt.Sprintf("%d:%d:%s", p.Range.Offset, p.Range.Length, h)}
			return nil
		}
	}
	if nativeIdentity(d.identity) {
		return nil
	}
	return errors.New("не найдена сохраняемая область persist для привязки устройства")
}
func (d *EDLReader) BindBackup(ctx context.Context, expected DeviceInfo) error {
	if Hash(d.geometry) != Hash(expected.Geometry) {
		return errors.New("геометрия бэкапа отличается")
	}
	if expected.Identity.Kind == "sahara" && d.chip.Hardware != "" {
		if d.chip != expected.Identity {
			return errors.New("Sahara identity differs from backup")
		}
		d.identity = d.chip
	}
	if expected.Identity.Kind == "storage" {
		var off, n uint64
		var hash string
		if _, e := fmt.Sscanf(expected.Identity.Hardware, "%d:%d:%s", &off, &n, &hash); e != nil || requireHash(hash) != nil {
			return errors.New("backup storage binding")
		}
		r := Range{"user", off, n}
		if !r.Valid(d.geometry) || n > 128<<20 {
			return errors.New("backup witness bounds")
		}
		actual, e := HashRange(ctx, d, r)
		if e != nil {
			return e
		}
		if actual != hash {
			return errors.New("бэкап принадлежит другому накопителю")
		}
		d.identity = expected.Identity
	}
	actual, e := d.Info(ctx)
	if e != nil {
		return e
	}
	if !EqualDevice(actual, expected) {
		return errors.New("бэкап принадлежит другому устройству/геометрии")
	}
	return nil
}

// BindOwnBackup also supports older Sahara-only manifests when reconnecting to
// their still-running programmer. The resulting identity remains storage-kind.
func (d *EDLReader) BindOwnBackup(ctx context.Context, root string, m BackupManifest) error {
	if m.Device.Identity.Kind == "sahara" && d.chip.Hardware == "" {
		id, e := backupStorageIdentity(ctx, root, m)
		if e != nil {
			return e
		}
		expected := m.Device
		expected.Identity = id
		return d.BindBackup(ctx, expected)
	}
	return d.BindBackup(ctx, m.Device)
}

func (d *EDLReader) Traffic() (uint64, uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.readBytes, d.writeBytes
}
func payloadName(file string) string {
	switch file {
	case "rootfs.ext4":
		return "системы"
	case "boot.img":
		return "загрузчика"
	case BootTZFile, BootHYPFile:
		return "загрузочной firmware"
	}
	return "сохранённых данных"
}
