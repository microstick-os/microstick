package flasherui

import (
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"microstick.dev/tools/internal/flasher"
)

// Layout/interaction fixtures use Russian. Bilingual tests exercise automatic
// locale detection.
func TestMain(m *testing.M) {
	os.Setenv("LC_ALL", "ru_RU.UTF-8")
	os.Setenv("LANGUAGE", "")
	os.Exit(m.Run())
}

func TestAutomaticLocaleScreens(t *testing.T) {
	for _, tc := range []struct{ locale, install, password, correction string }{
		{"en_US.UTF-8", "Install", "Wi-Fi and panel password", "Correct"},
		{"ru_RU.UTF-8", "Установка", "Пароль Wi-Fi и панели", "Исправить"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			t.Setenv("LC_ALL", tc.locale)
			u, screen := startUI(t)
			if !strings.Contains(screenText(screen), tc.install) {
				t.Fatal("menu does not use the system locale")
			}
			u.app.QueueUpdateDraw(func() { u.installForm(flasher.HardwareReport{}, nil) })
			if !strings.Contains(screenText(screen), tc.password) {
				t.Fatal("form does not use the system locale")
			}
			u.app.QueueUpdateDraw(func() {
				u.form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
			})
			text := screenText(screen)
			if !strings.Contains(text, tc.correction) || !strings.Contains(text, "8") {
				t.Fatal("validation message was not displayed")
			}
			if tc.locale == "en_US.UTF-8" && strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }) {
				t.Fatal("Russian text in the English interface")
			}
		})
	}
}
