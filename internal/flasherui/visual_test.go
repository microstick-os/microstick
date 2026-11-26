package flasherui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"microstick.dev/tools/internal/dev"
	"microstick.dev/tools/internal/flasher"
)

func screenLines(s tcell.SimulationScreen) []string {
	cells, w, h := screenSnapshot(s)
	lines := make([]string, h)
	for y := 0; y < h; y++ {
		var line strings.Builder
		for x := 0; x < w; x++ {
			runes := cells[y*w+x].Runes
			if len(runes) == 0 {
				line.WriteByte(' ')
			} else {
				line.WriteString(string(runes))
			}
		}
		lines[y] = line.String()
	}
	return lines
}

func TestBannerSharedOriginAndSingleCompactHeading(t *testing.T) {
	u, s := startUI(t)
	lines := screenLines(s)
	bannerLines := strings.Split(banner, "\n")
	width := 0
	for _, line := range bannerLines {
		width = max(width, len(line)) // The banner is ASCII.
	}
	_, screenWidth, _ := s.GetContents()
	left := (screenWidth - width) / 2
	for i, want := range bannerLines {
		if got := string([]rune(lines[i+1])[left : left+len(want)]); got != want {
			t.Fatalf("banner line %d moved: %q != %q", i, got, want)
		}
	}
	for _, size := range [][2]int{{40, 12}, {60, 20}, {80, 24}, {120, 40}} {
		s.SetSize(size[0], size[1])
		s.PostEventWait(tcell.NewEventResize(size[0], size[1]))
		flushUI(u)
		if got := strings.Count(screenText(s), "microstick"); got != 1 {
			t.Fatalf("%v: %d compact headings", size, got)
		}
		saveScreen(t, s, fmt.Sprintf("menu-%dx%d", size[0], size[1]))
	}
}

func TestBackupWindowsUseViewportAndExplainScope(t *testing.T) {
	u, s := startUI(t)
	for i := 1; i <= 28; i++ {
		path := filepath.Join(u.cfg.Paths.Work, "backups", fmt.Sprintf("flasher-202611%02dT010203.000000000Z", i))
		if e := os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
		if e := flasher.SaveJSON(filepath.Join(path, "BackupManifest.json"), flasher.BackupManifest{Version: flasher.Schema, Complete: i != 28}); e != nil {
			t.Fatal(e)
		}
	}
	items, e := dev.ListBackupSummaries(u.cfg.Paths)
	if e != nil || len(items) != 28 {
		t.Fatal(items, e)
	}
	u.app.QueueUpdateDraw(func() {
		u.cfg.Open = func(context.Context, flasher.Emit) (Session, error) {
			t.Error("local browsing opened USB")
			return Session{}, errors.New("no USB")
		}
	})
	for _, rollback := range []bool{false, true} {
		for _, size := range [][2]int{{40, 12}, {60, 20}, {80, 24}, {120, 40}} {
			s.SetSize(size[0], size[1])
			s.PostEventWait(tcell.NewEventResize(size[0], size[1]))
			u.app.QueueUpdateDraw(func() {
				if rollback {
					u.restoreList(items[1:])
				} else {
					u.backupManager()
				}
			})
			flushUI(u)
			if !rollback && strings.Contains(screenText(s), "Завершён") {
				t.Fatal("redundant completion status in backup list")
			}
			u.app.QueueUpdateDraw(func() {
				list := u.body.(*tview.List)
				_, secondary := list.GetItemText(list.GetItemCount() - 1)
				if secondary != "" {
					t.Error("Back has a redundant subtitle")
				}
				x, y, w, h := u.body.GetRect()
				if w != min(100, size[0]-4) || h < size[1]-10 || x < 0 || y+h > size[1] {
					t.Errorf("%v rollback=%t: cramped/outside viewport %d,%d %dx%d", size, rollback, x, y, w, h)
				}
				scope := "Все локальные бэкапы"
				if rollback {
					scope = "Только бэкапы подключённого устройства"
				}
				if !strings.Contains(u.status.GetText(false), scope) {
					t.Error("missing ownership scope")
				}
			})
			press(t, u, tcell.KeyEnd)
			if !strings.Contains(screenText(s), "Назад") {
				t.Fatal("last item is unreachable")
			}
			press(t, u, tcell.KeyHome)
			u.app.QueueUpdateDraw(func() {
				list := u.body.(*tview.List)
				selected, _ := list.GetItemText(list.GetCurrentItem())
				if !strings.Contains(screenText(s), selected) {
					t.Errorf("%v: selected backup is hidden: %s", size, selected)
				}
			})
			saveScreen(t, s, fmt.Sprintf("backups-%t-%dx%d", rollback, size[0], size[1]))
		}
	}
	u.app.QueueUpdateDraw(func() { u.restoreList(nil) })
	if !strings.Contains(screenText(s), "Подходящих бэкапов нет") {
		t.Fatal("empty rollback list unexplained")
	}
	u.app.QueueUpdateDraw(func() { u.backupManager() })
	press(t, u, tcell.KeyDown)
	if strings.Contains(screenText(s), items[0].Path) {
		t.Fatal("raw path crowded the list")
	}
	press(t, u, tcell.KeyF2)
	if !strings.Contains(screenText(s), items[0].Path) {
		t.Fatal("selected backup path missing from F2")
	}
	press(t, u, tcell.KeyEsc)
}

func TestBackupManagerDisplaysDeviceLabelsWithoutUSB(t *testing.T) {
	u, s := startUI(t)
	for i, identity := range []string{"private-unit-a", "private-unit-a", "private-unit-b"} {
		path := filepath.Join(u.cfg.Paths.Work, "backups", fmt.Sprintf("copy-%d", i))
		if e := os.MkdirAll(path, 0700); e != nil {
			t.Fatal(e)
		}
		m := flasher.BackupManifest{Version: flasher.Schema, Complete: true, Device: flasher.DeviceInfo{Identity: flasher.Identity{Kind: "simulator", Hardware: identity}}}
		if e := flasher.SaveJSON(filepath.Join(path, "BackupManifest.json"), m); e != nil {
			t.Fatal(e)
		}
	}
	u.app.QueueUpdateDraw(func() {
		u.cfg.Open = func(context.Context, flasher.Emit) (Session, error) {
			t.Error("device labels opened USB")
			return Session{}, errors.New("no USB")
		}
		u.backupManager()
	})
	labels := map[string]int{}
	var selectedLabel string
	u.app.QueueUpdateDraw(func() {
		list := u.body.(*tview.List)
		for i := 1; i <= 3; i++ {
			primary, secondary := list.GetItemText(i)
			label, _, ok := strings.Cut(primary, " · ")
			if !ok || !strings.HasPrefix(label, "Модем ") || secondary != "" {
				t.Error("device tag missing or compact row lost")
			}
			labels[label]++
			if i == 1 {
				selectedLabel = label
			}
		}
	})
	if len(labels) != 2 {
		t.Fatal("different devices indistinguishable", labels)
	}
	for _, count := range labels {
		if count != 1 && count != 2 {
			t.Fatal("same-device copies have different labels")
		}
	}
	if strings.Contains(screenText(s), "private-unit-") {
		t.Fatal("raw identity exposed")
	}
	saveScreen(t, s, "backup-device-labels")
	press(t, u, tcell.KeyDown)
	press(t, u, tcell.KeyF2)
	if !strings.Contains(screenText(s), selectedLabel) {
		t.Fatal("selected device tag missing from details")
	}
}

func TestStableAggregateProgressAndCoalescedRangeEvents(t *testing.T) {
	u, s := startUI(t)
	release, burstDone, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var draws atomic.Int64
	u.app.QueueUpdateDraw(func() {
		u.app.SetAfterDrawFunc(func(tcell.Screen) { draws.Add(1) })
		u.cfg.SelectDevice = func(dev.USBConnection) { close(finished) }
		u.work("Восстановление из бэкапа", func(context.Context) error {
			for i := 1; i <= 1000; i++ {
				stage := "write"
				if i%2 == 0 {
					stage = "readback"
				}
				u.progress(flasher.Progress{Stage: stage, Unit: "bytes", Done: uint64(i % 7), Total: 8, PlanDoneBytes: uint64(i) * 512, PlanTotalBytes: 1024000, Message: "Меняющийся блок"})
			}
			close(burstDone)
			<-release
			return nil
		}, func(error) { t.Error("cancelled operation reached completion callback") })
	})
	select {
	case <-burstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("range event producer blocked")
	}
	if n := draws.Load(); n > 20 {
		t.Fatalf("redrew %d times for tiny blocks", n)
	}
	for _, size := range [][2]int{{40, 12}, {60, 20}, {80, 24}, {120, 40}} {
		s.SetSize(size[0], size[1])
		s.PostEventWait(tcell.NewEventResize(size[0], size[1]))
		u.app.QueueUpdateDraw(func() { u.consumeProgress(time.Now()); u.renderProgress() })
		positions := func() map[string]int {
			got := map[string]int{}
			for y, line := range screenLines(s) {
				for _, label := range []string{"Прогресс:", "Объём:", "Скорость:", "Время:", "F2 подробности"} {
					if x := strings.Index(line, label); x >= 0 {
						got[label] = y*1000 + x
					}
				}
			}
			return got
		}
		before := positions()
		if len(before) != 5 {
			t.Fatalf("%v: clipped progress panel: %v\n%s", size, before, strings.Join(screenLines(s), "\n"))
		}
		if text := screenText(s); strings.Contains(text, "Меняющийся блок") || strings.Contains(text, "Выполняется…") {
			t.Fatal("range activity leaked into main layout")
		}
		u.progress(flasher.Progress{Stage: "write", Unit: "bytes", Done: 1 << 20, Total: 1 << 20, PlanDoneBytes: 128 << 20, PlanTotalBytes: 999 << 20})
		u.app.QueueUpdateDraw(func() { u.consumeProgress(time.Now()); u.renderProgress() })
		for label, position := range before {
			if positions()[label] != position {
				t.Fatal("progress label moved", label)
			}
		}
		u.app.QueueUpdateDraw(func() { saveScreen(t, s, fmt.Sprintf("progress-%dx%d", size[0], size[1])) })
	}
	u.app.QueueUpdateDraw(func() {
		if !strings.Contains(u.details.GetText(false), "readback 6/8") {
			t.Error("range details lost")
		}
	})
	press(t, u, tcell.KeyCtrlC)
	u.app.QueueUpdateDraw(func() { u.consumeProgress(time.Now()); u.renderProgress() })
	if !strings.Contains(screenText(s), "Отмена в безопасной точке") {
		t.Fatal("cancellation overwritten by progress")
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("work did not finish")
	}
}

func TestAggregateRateSurvivesBlockChangesAndBecomesIdle(t *testing.T) {
	u := &UI{}
	now := time.Now()
	put := func(stage string, n uint64, at time.Time) {
		u.pendingProgress = &flasher.Progress{Stage: stage, Unit: "bytes", Done: 0, Total: 512, PlanDoneBytes: n, PlanTotalBytes: 100 << 20}
		u.consumeProgress(at)
	}
	put("write", 10<<20, now)
	put("readback", 11<<20, now.Add(time.Second))
	if u.stageRate != 1<<20 {
		t.Fatal("rate reset at the block boundary", u.stageRate)
	}
	u.consumeProgress(now.Add(4 * time.Second))
	if u.stageRate != 0 {
		t.Fatal("idle rate did not expire", u.stageRate)
	}
	put("write", 5<<20, now.Add(5*time.Second))
	if u.stageRate != 0 {
		t.Fatal("resumed counter underflow")
	}
}

func TestDiscoveryFailureUsesNaturalTextAndKeepsCauseInDetails(t *testing.T) {
	u, s := startUI(t)
	cause := errors.New("native-list diagnostic sentinel")
	err := &DeviceDiscoveryError{Cause: cause}
	if !errors.Is(err, cause) {
		t.Fatal("lost discovery cause")
	}
	u.app.QueueUpdateDraw(func() { u.failed(err) })
	text := screenText(s)
	if !strings.Contains(text, "Не удалось проверить подключение") || !strings.Contains(text, "модема.") || strings.Contains(text, "перечислить") || strings.Contains(text, ";") || strings.Contains(text, cause.Error()) {
		t.Fatal("unnatural or technical dialog", text)
	}
	saveScreen(t, s, "usb-failure")
	press(t, u, tcell.KeyF2)
	if !strings.Contains(screenText(s), cause.Error()) {
		t.Fatal("technical cause missing from F2")
	}
}

func TestCancelReturnsToMenuWithoutDialog(t *testing.T) {
	for _, outcome := range []string{"cancelled", "safely-finished", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			u, s := startUI(t)
			release, settled := make(chan struct{}), make(chan struct{}, 1)
			signal := func() {
				select {
				case settled <- struct{}{}:
				default:
				}
			}
			u.app.QueueUpdateDraw(func() {
				u.cfg.SelectDevice = func(dev.USBConnection) { signal() }
				u.work("Подготовка файлов для установки", func(context.Context) error {
					<-release // An in-flight operation finishes at its safe boundary.
					switch outcome {
					case "cancelled":
						return fmt.Errorf("prepare: %w", context.Canceled)
					case "failure":
						return errors.New("actual operation failure")
					}
					return nil
				}, func(e error) { u.failed(e); signal() })
			})
			press(t, u, tcell.KeyCtrlC)
			u.app.QueueUpdateDraw(func() {
				if !u.active {
					t.Error("returned to menu before reaching a safe boundary")
				}
			})
			close(release)
			select {
			case <-settled:
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation did not return")
			}
			flushUI(u)
			u.app.QueueUpdateDraw(func() {
				page, _ := u.pages.GetFrontPage()
				if outcome == "failure" {
					if page != "dialog" || !strings.Contains(screenText(s), "actual operation failure") {
						t.Error("cancellation hid a real operation failure")
					}
				} else if page != "main" || !strings.Contains(screenText(s), "Главное меню") || strings.Contains(screenText(s), "Операция отменена") {
					t.Error("cancellation displayed a dialog")
				}
			})
		})
	}
}
