package flasherui

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"microstick.dev/tools/internal/l10n"
	"strings"
	"time"
)

const banner = `           _                    _   _      _
 _ __ ___ (_) ___ _ __ ___  ___| |_(_) ___| | __
| '_ ` + "`" + ` _ \| |/ __| '__/ _ \/ __| __| |/ __| |/ /
| | | | | | | (__| | | (_) \__ \ |_| | (__|   <
|_| |_| |_|_|\___|_|  \___/|___/\__|_|\___|_|\_\`

func init() {
	tview.Styles.PrimitiveBackgroundColor = tcell.NewHexColor(0x17191c)
	tview.Styles.ContrastBackgroundColor = tcell.NewHexColor(0x292d32)
	tview.Styles.PrimaryTextColor = tcell.NewHexColor(0xe4e6e9)
	tview.Styles.BorderColor = tcell.NewHexColor(0x7a828d)
	tview.Styles.TitleColor = tcell.NewHexColor(0xe4e6e9)
}

type shell struct {
	*tview.Box
	body          tview.Primitive
	status        *tview.TextView
	wide, working bool
	full          bool
	statusText    func(int, int) string
}

func (s *shell) Draw(screen tcell.Screen) {
	s.Box.DrawForSubclass(screen, s)
	x, y, w, h := s.GetInnerRect()
	width := min(max(1, w-4), 76)
	if s.wide || s.working {
		width = min(max(1, w-4), 100)
	}
	left := x + (w-width)/2
	top := y + min(1, max(0, h-1))
	if s.full {
		s.body.SetRect(left, top, width, max(1, y+h-top-1))
		s.body.Draw(screen)
		return
	}
	if !s.wide && !s.working && w >= 64 && h >= 23 {
		lines := strings.Split(banner, "\n")
		blockWidth := 0
		for _, line := range lines {
			blockWidth = max(blockWidth, tview.TaggedStringWidth(line))
		}
		for i, line := range lines {
			tview.Print(screen, line, left+(width-blockWidth)/2, top+i, blockWidth, tview.AlignLeft, tcell.NewHexColor(0xa0a9b5))
		}
		top += 7
	}
	if s.statusText != nil {
		s.status.SetText(s.statusText(max(1, width-2), h))
	}
	s.status.SetWrap(!s.working)
	lines := len(tview.WordWrap(s.status.GetText(false), max(1, width-2)))
	reserve := 4
	if s.working {
		reserve = 1 // The working screen has no body controls below its status.
	}
	sh := min(max(4, lines+2), max(1, y+h-top-reserve))
	s.status.SetRect(left, top, width, sh)
	s.status.Draw(screen)
	top += sh + 1
	available := max(1, y+h-top-1)
	height := min(available, 19)
	if list, ok := s.body.(*tview.List); ok && !s.wide {
		width = min(max(1, w-4), 52)
		left = x + (w-width)/2
		rows := 1
		for i := 0; i < list.GetItemCount(); i++ {
			if _, secondary := list.GetItemText(i); secondary != "" {
				rows = 2
				break
			}
		}
		height = min(available, rows*list.GetItemCount()+2)
	}
	if s.wide {
		height = available
	}
	bodyTop := top
	if !s.wide && !s.working {
		bodyTop += max(0, (available-height)/2)
	}
	s.body.SetRect(left, bodyTop, width, height)
	s.body.Draw(screen)
}
func duration(d time.Duration) string {
	n := int64(d / time.Second)
	if n < 0 {
		n = 0
	}
	if n < 60 {
		return fmt.Sprintf(l10n.Text("ui.duration_seconds"), n)
	}
	if n < 3600 {
		return fmt.Sprintf(l10n.Text("ui.duration_minutes"), n/60, n%60)
	}
	return fmt.Sprintf(l10n.Text("ui.duration_hours"), n/3600, n/60%60, n%60)
}

func (s *shell) Focus(delegate func(tview.Primitive)) { delegate(s.body) }
func (s *shell) HasFocus() bool                       { return s.body.HasFocus() }
func (s *shell) Blur()                                { s.body.Blur() }

func (s *shell) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return s.body.InputHandler()
}
func (s *shell) PasteHandler() func(string, func(tview.Primitive)) { return s.body.PasteHandler() }
func (s *shell) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return s.body.MouseHandler()
}
