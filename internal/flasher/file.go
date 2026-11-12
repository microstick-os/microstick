package flasher

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
)

// FileDevice never opens device nodes. Writable files are an explicit simulator,
// not a way to route live disks around the profile gate.
type FileDevice struct {
	mu       sync.Mutex
	files    map[string]*os.File
	info     DeviceInfo
	writable bool
}

func OpenFiles(paths map[string]string, id string, simulation bool) (*FileDevice, error) {
	d := &FileDevice{files: map[string]*os.File{}, writable: simulation, info: DeviceInfo{Identity: Identity{"simulator", id}, Geometry: Geometry{512, map[string]uint64{}}}}
	if id == "" {
		return nil, errors.New("требуется идентификатор набора файлов")
	}
	for area, p := range paths {
		flags := syscall.O_RDONLY
		if simulation {
			flags = syscall.O_RDWR
		}
		fd, e := syscall.Open(p, flags|syscall.O_NOFOLLOW, 0)
		if e != nil {
			d.Close()
			return nil, e
		}
		f := os.NewFile(uintptr(fd), p)
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() {
			f.Close()
			d.Close()
			return nil, errors.New("разрешены только обычные файлы образов")
		}
		if simulation {
			if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
				f.Close()
				d.Close()
				return nil, e
			}
		}
		d.files[area] = f
		d.info.Geometry.Areas[area] = uint64(st.Size())
	}
	if e := ValidateInfo(d.info); e != nil {
		d.Close()
		return nil, e
	}
	return d, nil
}
func (d *FileDevice) Close() error {
	for _, f := range d.files {
		f.Close()
	}
	return nil
}
func (d *FileDevice) Info(ctx context.Context) (DeviceInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return DeviceInfo{}, e
	}
	g := Geometry{512, map[string]uint64{}}
	for area, f := range d.files {
		st, e := f.Stat()
		if e != nil {
			return DeviceInfo{}, e
		}
		g.Areas[area] = uint64(st.Size())
	}
	return DeviceInfo{d.info.Identity, g}, nil
}
func (d *FileDevice) Read(ctx context.Context, r Range, w io.Writer) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !r.Valid(d.info.Geometry) {
		return errors.New("чтение вне диапазона")
	}
	f := d.files[r.Area]
	buf := make([]byte, Chunk)
	for pos := uint64(0); pos < r.Length; {
		if e := ctx.Err(); e != nil {
			return e
		}
		n := min(uint64(len(buf)), r.Length-pos)
		if _, e := f.ReadAt(buf[:n], int64(r.Offset+pos)); e != nil {
			return e
		}
		written, e := w.Write(buf[:n])
		if e != nil {
			return e
		}
		if uint64(written) != n {
			return io.ErrShortWrite
		}
		pos += n
	}
	return nil
}
func (d *FileDevice) Write(ctx context.Context, r Range, src io.Reader) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.writable {
		return errors.New("транспорт открыт только для чтения")
	}
	if !r.Valid(d.info.Geometry) {
		return errors.New("запись вне диапазона")
	}
	buf := make([]byte, Chunk)
	for pos := uint64(0); pos < r.Length; {
		if e := ctx.Err(); e != nil {
			return e
		}
		n := min(uint64(len(buf)), r.Length-pos)
		if _, e := io.ReadFull(src, buf[:n]); e != nil {
			return e
		}
		if _, e := d.files[r.Area].WriteAt(buf[:n], int64(r.Offset+pos)); e != nil {
			return e
		}
		pos += n
	}
	return nil
}
func (d *FileDevice) Sync(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	for _, f := range d.files {
		if e := f.Sync(); e != nil {
			return e
		}
	}
	return nil
}
