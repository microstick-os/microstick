package flasherui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"microstick.dev/tools/internal/dev"
	"microstick.dev/tools/internal/flasher"
	"microstick.dev/tools/internal/l10n"
)

func startUI(t *testing.T) (*UI, tcell.SimulationScreen) {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	screen.SetSize(120, 40)
	u := New(Config{Paths: dev.Paths{Work: t.TempDir()}})
	u.SetScreen(screen)
	ready := make(chan struct{})
	var once sync.Once
	u.app.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- u.Run() }()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("UI did not render")
	}
	t.Cleanup(func() {
		u.app.Stop()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("UI did not stop")
		}
	})
	flushUI(u) // AfterDraw fires before Show; wait for a complete physical frame.
	return u, screen
}

// GetContents exposes the simulation's mutable front buffer. Copy it while
// holding the same lock as Show/SetSize, so checks see complete rendered frames.
func screenSnapshot(s tcell.SimulationScreen) ([]tcell.SimCell, int, int) {
	cells, w, h := s.GetContents()
	locker := s.(sync.Locker)
	locker.Lock()
	defer locker.Unlock()
	copyCells := make([]tcell.SimCell, len(cells))
	for i, c := range cells {
		copyCells[i] = c
		copyCells[i].Runes = append([]rune(nil), c.Runes...)
		copyCells[i].Bytes = append([]byte(nil), c.Bytes...)
	}
	return copyCells, w, h
}

func screenText(s tcell.SimulationScreen) string {
	contents, _, _ := screenSnapshot(s)
	var b strings.Builder
	for _, c := range contents {
		for _, r := range c.Runes {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func flushUI(u *UI) { u.app.QueueUpdateDraw(func() {}) }

func press(t *testing.T, u *UI, key tcell.Key) {
	t.Helper()
	seen := make(chan struct{})
	old := u.app.GetInputCapture()
	u.app.QueueUpdateDraw(func() {
		u.app.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
			defer close(seen)
			u.app.SetInputCapture(old)
			if old != nil {
				return old(e)
			}
			return e
		})
	})
	u.app.QueueEvent(tcell.NewEventKey(key, 0, tcell.ModNone))
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("key not handled")
	}
	flushUI(u)
}
func TestMenuResizeCancelAndMaskedPasswords(t *testing.T) {
	u, s := startUI(t)
	flushUI(u)
	for _, label := range []string{"Установка", "Обновление", "Бэкапы", "Откат", "Исследовать устройство"} {
		if !strings.Contains(screenText(s), label) {
			t.Fatal("missing menu item", label)
		}
	}
	s.SetSize(70, 20)
	s.PostEventWait(tcell.NewEventResize(70, 20))
	flushUI(u)
	u.app.QueueUpdateDraw(func() {
		u.credentials(flasher.HardwareReport{}, "", "")
		_, p := u.pages.GetFrontPage()
		layout := p.(*shell)
		form := layout.body.(*tview.Form)
		for _, i := range []int{1, 2} {
			form.GetFormItem(i).(*tview.InputField).SetText("PRIVATE-UI-SECRET")
		}
	})
	if strings.Contains(screenText(s), "PRIVATE-UI-SECRET") {
		t.Fatal("password field is not masked")
	}
	press(t, u, tcell.KeyEsc)
	if !strings.Contains(screenText(s), "Главное меню") {
		t.Fatal("escape did not return")
	}
}
func TestSupportLinkVisible(t *testing.T) {
	u, s := startUI(t)
	path := filepath.Join(u.cfg.Paths.Work, "board-report-20261111.tar.gz")
	u.app.QueueUpdateDraw(func() { u.support(flasher.Inspection{Archive: path}, u.menu) })
	_ = s
	u.app.QueueUpdateDraw(func() {
		_, p := u.pages.GetFrontPage()
		text := p.(*shell).body.(*tview.Flex).GetItem(0).(*tview.TextView).GetText(false)
		if !strings.Contains(text, "issue") || !strings.Contains(text, path) {
			t.Error("support path/instruction missing")
		}
	})
}

func TestSupportArchiveActionsAndSmallScreen(t *testing.T) {
	u, s := startUI(t)
	path := "/tmp/Каталог с пробелами/board-report-20261112.tar.gz"
	copied := make(chan string, 1)
	opened := make(chan string, 1)
	u.app.QueueUpdateDraw(func() {
		u.cfg.CopyPath = func(_ context.Context, p string) (string, error) {
			copied <- p
			return "Путь скопирован", nil
		}
		u.cfg.OpenFolder = func(_ context.Context, p string) error {
			opened <- p
			return errors.New("Файловый менеджер недоступен")
		}
		u.support(flasher.Inspection{Archive: path}, u.menu)
	})
	for _, size := range [][2]int{{80, 24}, {60, 20}, {40, 12}} {
		s.SetSize(size[0], size[1])
		s.PostEventWait(tcell.NewEventResize(size[0], size[1]))
		flushUI(u)
		text := screenText(s)
		for _, label := range []string{"Скопировать путь", "Открыть папку", "В меню"} {
			if !strings.Contains(text, label) {
				t.Fatal("archive action clipped", size, label)
			}
		}
		if strings.Contains(text, "лутбокс") {
			t.Fatal("old support wording")
		}
		saveScreen(t, s, fmt.Sprintf("support-%dx%d", size[0], size[1]))
	}
	press(t, u, tcell.KeyEnter)
	select {
	case p := <-copied:
		if p != path {
			t.Fatal("copied wrapped path")
		}
	case <-time.After(time.Second):
		t.Fatal("copy not invoked")
	}
	flushUI(u)
	press(t, u, tcell.KeyTab)
	press(t, u, tcell.KeyEnter)
	select {
	case p := <-opened:
		if p != filepath.Dir(path) {
			t.Fatal("opened archive instead of folder")
		}
	case <-time.After(time.Second):
		t.Fatal("open not invoked")
	}
	flushUI(u)
	press(t, u, tcell.KeyEscape)
	if !strings.Contains(screenText(s), "Установка") {
		t.Fatal("support did not return to menu")
	}
}

func TestLiveTimerAndByteProgressWithoutNewEvents(t *testing.T) {
	u, _ := startUI(t)
	status := func() (text string) { u.app.QueueUpdateDraw(func() { text = u.status.GetText(false) }); return }
	release := make(chan struct{})
	finished := make(chan struct{})
	u.app.QueueUpdateDraw(func() {
		u.work("Бэкап", func(ctx context.Context) error {
			u.progress(flasher.Progress{Stage: "backup", Done: 1 << 20, Total: 8 << 20, Overall: 23, Message: "Копирование"})
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, func(error) { close(finished) })
	})
	flushUI(u)
	u.app.QueueUpdateDraw(func() { u.started = time.Now().Add(-211 * time.Second); u.renderProgress() })
	if text := status(); !strings.Contains(text, "МиБ") || !strings.Contains(text, "3 мин 31 сек") {
		t.Fatal("byte progress/timer missing")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(status(), "3 мин 32 сек") {
		time.Sleep(20 * time.Millisecond)
		flushUI(u)
	}
	if !strings.Contains(status(), "3 мин 32 сек") {
		t.Fatal("timer froze without a new progress event")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("work did not finish")
	}
}

func TestRecognizedBoardPreparationErrorIsNotUnknownBoard(t *testing.T) {
	u, s := startUI(t)
	u.app.QueueUpdateDraw(func() {
		u.support(flasher.Inspection{Report: flasher.HardwareReport{Resolution: flasher.Resolution{Profile: "microstick-hmuf02-system-linux17", Status: "unsupported", Reasons: []string{"неполный родной DSP"}}}, Archive: "/tmp/support.tar.gz"}, u.menu)
	})
	text := screenText(s)
	if !strings.Contains(text, "Плата распознана") || !strings.Contains(text, "неполный родной DSP") || strings.Contains(text, "Плата неизвестна") {
		t.Fatal("preparation error hid recognized board")
	}
}

type emptyDevice struct{}

func (emptyDevice) Info(context.Context) (flasher.DeviceInfo, error) {
	return flasher.DeviceInfo{Identity: flasher.Identity{Kind: "sahara", Hardware: "PRIVATE-HARDWARE"}, Geometry: flasher.Geometry{SectorSize: 512, Areas: map[string]uint64{"user": 1024 * 1024, "boot1": 4096, "boot2": 4096}}}, nil
}
func (emptyDevice) Read(context.Context, flasher.Range, io.Writer) error {
	return errors.New("bad GPT")
}

type closer struct{}

func (closer) Close() error { return nil }
func TestSessionSurvivesCompletedStage(t *testing.T) {
	u, _ := startUI(t)
	var lifetime context.Context
	u.cfg.Open = func(ctx context.Context, _ flasher.Emit) (Session, error) {
		lifetime = ctx
		return Session{Device: emptyDevice{}, Closer: closer{}}, nil
	}
	stage, cancel := context.WithCancel(context.Background())
	session, e := u.open(stage)
	if e != nil {
		t.Fatal(e)
	}
	u.session = session
	cancel()
	if lifetime.Err() != nil {
		t.Fatal("native session died at the end of inspection")
	}
	u.closeSession()
	if lifetime.Err() == nil {
		t.Fatal("session context leaked after exit")
	}
}

func TestInstallOpensDeviceWithoutAnyModelConfirmation(t *testing.T) {
	u, s := startUI(t)
	opened := make(chan struct{}, 1)
	u.app.QueueUpdateDraw(func() {
		u.cfg.Open = func(context.Context, flasher.Emit) (Session, error) {
			opened <- struct{}{}
			return Session{}, errors.New("fixture connection failure")
		}
	})
	press(t, u, tcell.KeyEnter)
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("installation stopped before opening device")
	}
	flushUI(u)
	for _, label := range []string{"Подтвердите физически", "подтверждена", "confirmed-board", "y/N"} {
		if strings.Contains(screenText(s), label) {
			t.Fatal("manual barrier", label)
		}
	}
}

func TestTwoUSBConnectionsUseConnectionPicker(t *testing.T) {
	u, s := startUI(t)
	selected := ""
	u.app.QueueUpdateDraw(func() {
		u.cfg.Devices = func(context.Context) ([]dev.USBConnection, error) {
			return []dev.USBConnection{{Path: "@1-2", Transport: "edl"}, {Path: "@1-3", Transport: "adb"}}, nil
		}
		u.cfg.SelectDevice = func(v dev.USBConnection) { selected = v.Path }
	})
	press(t, u, tcell.KeyEnter)
	flushUI(u)
	// Wait for the asynchronous enumeration to publish its selection screen.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(screenText(s), "Выберите USB") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		flushUI(u)
	}
	if !strings.Contains(screenText(s), "USB @1-2") || !strings.Contains(screenText(s), "USB @1-3") {
		t.Fatal("USB connections not listed")
	}
	if selected != "" {
		t.Fatal("multiple devices were silently selected")
	}
}

func TestAutomaticReconnectIsBoundedAndPreservesDeviceBinding(t *testing.T) {
	for _, kind := range []string{"recover", "limit", "integrity", "swapped", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			u, _ := startUI(t)
			expected, _ := emptyDevice{}.Info(context.Background())
			runs, opens := 0, 0
			u.cfg.Open = func(context.Context, flasher.Emit) (Session, error) {
				opens++
				device := flasher.Reader(emptyDevice{})
				if kind == "swapped" {
					device = otherDevice{}
				}
				return Session{Device: device, Closer: closer{}}, nil
			}
			u.session = Session{Device: emptyDevice{}, Closer: closer{}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			e := u.connectedOperation(ctx, &expected, func() error {
				runs++
				if kind == "integrity" {
					return errors.New("payload hash differs")
				}
				if kind == "recover" && runs == 2 {
					return nil
				}
				return flasher.ErrEDLTransport
			})
			u.closeSession()
			switch kind {
			case "recover":
				if e != nil || runs != 2 || opens != 1 {
					t.Fatal("did not recover", e, runs, opens)
				}
			case "limit":
				if e == nil || runs != 3 || opens != 2 {
					t.Fatal("retry not bounded", e, runs, opens)
				}
			case "integrity":
				if e == nil || opens != 0 {
					t.Fatal("retried integrity failure")
				}
			case "swapped":
				if e == nil || runs != 1 || opens != 1 {
					t.Fatal("changed device continued", e, runs, opens)
				}
			case "cancel":
				if e != context.Canceled || runs != 0 || opens != 0 {
					t.Fatal("cancel continued", e)
				}
			}
		})
	}
}

type otherDevice struct{ emptyDevice }

func (otherDevice) Info(ctx context.Context) (flasher.DeviceInfo, error) {
	v, e := emptyDevice{}.Info(ctx)
	v.Identity.Hardware = "other-unit"
	return v, e
}

func TestLiveTransportStepOffersContinueWithoutRollback(t *testing.T) {
	u, screen := startUI(t)
	before := strings.Count(screenText(screen), l10n.Text("ui.menu_rollback"))
	u.app.QueueUpdateDraw(func() { u.failed(&ConnectionStep{Message: l10n.Text("ui.active_tree_switch_edl")}) })
	flushUI(u)
	text := screenText(screen)
	if !strings.Contains(text, "EDL") || strings.Count(text, l10n.Text("ui.menu_rollback")) > before {
		t.Fatal("transport step treated as a write failure", text)
	}
}

func TestDiscoveryCaptureProgressCancelAndRetry(t *testing.T) {
	u, screen := startUI(t)
	var searches, opens atomic.Int32
	capture := make(chan struct{}, 3)
	selected := ""
	u.app.QueueUpdateDraw(func() {
		u.cfg.Devices = func(ctx context.Context) ([]dev.USBConnection, error) {
			searches.Add(1)
			return []dev.USBConnection{{Path: "@1-4", Transport: "adb", State: "device"}}, nil
		}
		u.cfg.SelectDevice = func(d dev.USBConnection) { selected = d.Path }
		u.cfg.Open = func(ctx context.Context, emit flasher.Emit) (Session, error) {
			n := opens.Add(1)
			emit(flasher.Progress{Stage: "active-dtb", Message: "Чтение дерева: узлов 251, свойств 3001"})
			capture <- struct{}{}
			if n == 1 {
				<-ctx.Done()
				return Session{}, ctx.Err()
			}
			return Session{}, &ConnectionStep{Message: l10n.Text("ui.active_tree_switch_edl")}
		}
		u.start(4)
	})
	select {
	case <-capture:
	case <-time.After(time.Second):
		t.Fatal("capture did not start")
	}
	flushUI(u)
	if !strings.Contains(screenText(screen), "3001") || strings.Contains(screenText(screen), l10n.Text("ui.looking_for_usb_connections")) {
		t.Fatal("capture hidden behind discovery")
	}
	press(t, u, tcell.KeyCtrlC)
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(screenText(screen), l10n.Text("ui.menu_inspect")) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		flushUI(u)
	}
	u.app.QueueUpdateDraw(func() {
		if selected != "" {
			t.Error("selection not cleared after cancel")
		}
		u.start(4)
	})
	select {
	case <-capture:
	case <-time.After(time.Second):
		t.Fatal("retry did not start")
	}
	flushUI(u)
	if searches.Load() != 2 || opens.Load() != 2 {
		t.Fatal("retry did not rediscover", searches.Load(), opens.Load())
	}
}

func TestNoConnectionsDoesNotOpenOrCapture(t *testing.T) {
	u, screen := startUI(t)
	u.app.QueueUpdateDraw(func() {
		u.cfg.Devices = func(context.Context) ([]dev.USBConnection, error) { return nil, nil }
		u.cfg.Open = func(context.Context, flasher.Emit) (Session, error) {
			t.Error("opened absent device")
			return Session{}, errors.New("absent")
		}
		u.start(4)
	})
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(screenText(screen), strings.Split(l10n.Text("ui.no_usb_connections"), ".")[0]) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		flushUI(u)
	}
	if !strings.Contains(screenText(screen), strings.Split(l10n.Text("ui.no_usb_connections"), ".")[0]) {
		t.Fatal("empty discovery did not settle")
	}
}

func TestInstallResearchStepKeepsActionAndOffersAutomaticRetry(t *testing.T) {
	u, screen := startUI(t)
	var retried int
	u.app.QueueUpdateDraw(func() {
		u.deviceChecked = true
		u.retry = func() {
			retried++
			if u.deviceChecked {
				t.Error("research retry did not re-enumerate")
			}
		}
		u.failed(&flasher.ResearchRequired{})
	})
	flushUI(u)
	if strings.Contains(screenText(screen), "Плата неизвестна") || !strings.Contains(screenText(screen), "ADB") {
		t.Fatal("research request became unknown board")
	}
	press(t, u, tcell.KeyEnter)
	if retried != 1 {
		t.Fatal("install continuation lost", retried)
	}
}

func TestUnsupportedResearchShowsActualCauseWithoutMatchedProfile(t *testing.T) {
	u, _ := startUI(t)
	cause := "synthetic component binding is missing"
	u.app.QueueUpdateDraw(func() {
		u.support(flasher.Inspection{Archive: "/tmp/report.tar.gz", Report: flasher.HardwareReport{Resolution: flasher.Resolution{Status: "unsupported", Reasons: []string{cause}}}}, u.menu)
		_, p := u.pages.GetFrontPage()
		text := p.(*shell).body.(*tview.Flex).GetItem(0).(*tview.TextView).GetText(false)
		if !strings.Contains(text, cause) {
			t.Error("actual automatic profiling cause hidden")
		}
	})
}
