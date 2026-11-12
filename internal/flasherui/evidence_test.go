package flasherui

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"html"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayoutEvidence(t *testing.T) {
	u, screen := startUI(t)
	for _, size := range [][2]int{{80, 24}, {60, 20}, {120, 40}} {
		screen.SetSize(size[0], size[1])
		screen.PostEventWait(tcell.NewEventResize(size[0], size[1]))
		flushUI(u)
		if !strings.Contains(screenText(screen), "Установка") {
			t.Fatal("main menu clipped")
		}
		saveScreen(t, screen, fmt.Sprintf("menu-%dx%d", size[0], size[1]))
	}
	u.app.QueueUpdateDraw(func() { u.installForm(flasher.HardwareReport{}, nil) })
	saveScreen(t, screen, "installation")
	u.app.QueueUpdateDraw(func() { u.backupManager() })
	saveScreen(t, screen, "backups")
}
func saveScreen(t *testing.T, screen tcell.SimulationScreen, name string) {
	t.Helper()
	dir := os.Getenv("MICROSTICK_UI_EVIDENCE")
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("evidence path must be external and absolute")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	cells, w, h := screenSnapshot(screen)
	var svg, plain strings.Builder
	fmt.Fprintf(&svg, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><rect width="100%%" height="100%%" fill="#17191c"/><g font-family="DejaVu Sans Mono,monospace" font-size="14">`, w*9, h*18, w*9, h*18)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			text := string(c.Runes)
			if text == "" {
				text = " "
			}
			plain.WriteString(text)
			fg, bg, _ := c.Style.Decompose()
			if bg != tcell.ColorDefault {
				fmt.Fprintf(&svg, `<rect x="%d" y="%d" width="9" height="18" fill="#%06x"/>`, x*9, y*18, bg.Hex())
			}
			if strings.TrimSpace(text) != "" {
				fmt.Fprintf(&svg, `<text x="%d" y="%d" fill="#%06x">%s</text>`, x*9, y*18+14, fg.Hex(), html.EscapeString(text))
			}
		}
		plain.WriteByte('\n')
	}
	svg.WriteString("</g></svg>\n")
	if e := os.WriteFile(filepath.Join(dir, name+".svg"), []byte(svg.String()), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(plain.String()), 0600); e != nil {
		t.Fatal(e)
	}
}
