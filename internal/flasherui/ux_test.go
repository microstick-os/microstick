package flasherui

import (
	"context"
	"errors"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"microstick.dev/tools/internal/dev"
	"microstick.dev/tools/internal/flasher"
	"strings"
	"testing"
	"time"
)

func TestDurationBoundaries(t *testing.T) {
	for seconds, want := range map[int]string{0: "0 сек", 59: "59 сек", 60: "1 мин 0 сек", 70: "1 мин 10 сек", 960: "16 мин 0 сек", 3661: "1 ч 1 мин 1 сек"} {
		if got := duration(time.Duration(seconds) * time.Second); got != want {
			t.Fatalf("%d: %s", seconds, got)
		}
	}
}
func TestFormDefaultsInlineErrorAndSmallResize(t *testing.T) {
	u, s := startUI(t)
	var id string
	report := flasher.HardwareReport{GPT: flasher.PartitionTable{Partitions: []flasher.Partition{{Name: "system", GUID: "00000001-1111-2222-3333-444444444444"}}}}
	u.app.QueueUpdateDraw(func() {
		u.installForm(report, nil)
		id = u.draftID
		u.form.GetFormItem(1).(*tview.InputField).SetText("short")
		u.form.SetFocus(4)
		u.form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	})
	if !strings.Contains(screenText(s), "Пароль Wi-Fi и панели") {
		t.Fatal("error not visible")
	}
	press(t, u, tcell.KeyEnter)
	u.app.QueueUpdateDraw(func() {
		if u.form.GetFormItem(1).(*tview.InputField).GetText() != "short" {
			t.Error("input lost")
		}
		focused, _ := u.form.GetFocusedItemIndex()
		if focused != 1 {
			t.Error("focus not restored", focused)
		}
		if got := u.form.GetFormItem(0).(*tview.InputField).GetText(); got != dev.DefaultSSID(id) || !strings.HasPrefix(got, "microstick-") || len(got) != 19 {
			t.Error("default SSID", got)
		}
	})
	for _, size := range [][2]int{{80, 24}, {60, 20}, {120, 40}, {40, 12}} {
		s.SetSize(size[0], size[1])
		s.PostEventWait(tcell.NewEventResize(size[0], size[1]))
		flushUI(u)
	}
	press(t, u, tcell.KeyEsc)
	u.app.QueueUpdateDraw(func() {
		u.installForm(report, nil)
		if u.draftID != id {
			t.Error("UUID changed while correcting form")
		}
	})
	var field *dev.FormError
	if !errors.As(dev.ValidateWizardOptions(dev.WizardOptions{}), &field) {
		t.Fatal("missing field error")
	}
}
func TestBackupBrowsingDoesNotOpenUSB(t *testing.T) {
	u, s := startUI(t)
	u.app.QueueUpdateDraw(func() {
		u.cfg.Open = func(_ context.Context, _ flasher.Emit) (Session, error) {
			t.Error("USB opened for backup browsing")
			return Session{}, errors.New("no USB")
		}
		u.backupManager()
	})
	if !strings.Contains(screenText(s), "Создать бэкап") {
		t.Fatal("backup manager unavailable")
	}
}
