package flasherui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"microstick.dev/tools/internal/flasher"
	"microstick.dev/tools/internal/l10n"
)

func TestRegistryStatusAndImportDoNotOpenDevice(t *testing.T) {
	u, screen := startUI(t)
	client, e := flasher.NewRegistryClient(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	u.app.QueueUpdateDraw(func() {
		u.cfg.Registry = client
		u.cfg.FlasherVersion = flasher.EngineVersion
		u.cfg.Open = func(context.Context, flasher.Emit) (Session, error) {
			t.Error("registry UI opened a device")
			return Session{}, errors.New("no device")
		}
		u.menu()
	})
	flushUI(u)
	if !strings.Contains(screenText(screen), l10n.Text("registry.menu")) {
		t.Fatal("registry menu missing")
	}
	u.app.QueueUpdateDraw(u.registryStatus)
	flushUI(u)
	if !strings.Contains(screenText(screen), l10n.Text("registry.source_bundled")) {
		t.Fatal("bootstrap source missing")
	}
	u.app.QueueUpdateDraw(func() { u.pages.RemovePage("dialog"); u.registryImport() })
	flushUI(u)
	if !strings.Contains(screenText(screen), l10n.Text("registry.path")) {
		t.Fatal("import path missing")
	}
}
