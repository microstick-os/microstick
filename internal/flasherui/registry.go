package flasherui

import (
	"context"
	"fmt"
	"github.com/rivo/tview"
	"microstick.dev/tools/internal/l10n"
)

func (u *UI) registryStatus() {
	s := u.cfg.Registry.Current()
	date := s.Published()
	if date == "" {
		date = l10n.Text("registry.bundled")
	}
	message := fmt.Sprintf(l10n.Text("registry.status"), l10n.Text("registry.source_"+s.Source()), s.Sequence(), date)
	if u.cfg.Registry.Warning() != "" {
		message += "\n\n" + l10n.Text("registry.cache_notice")
	}
	u.modal(message, []string{l10n.Text("registry.refresh"), l10n.Text("registry.import"), l10n.Text("ui.back")}, func(i int) {
		switch i {
		case 0:
			u.work(l10n.Text("registry.refresh"), func(ctx context.Context) error { return u.cfg.Registry.Refresh(ctx) }, func(e error) {
				if e != nil {
					fmt.Fprintln(u.details, e)
				}
				u.cfg.Profiles = u.cfg.Registry.Current().Profiles(u.cfg.FlasherVersion, "", nil)
				u.registryStatus()
			})
		case 1:
			u.registryImport()
		default:
			u.menu()
		}
	})
}
func (u *UI) registryImport() {
	path := ""
	f := tview.NewForm()
	f.SetBorder(true).SetTitle(l10n.Text("registry.import"))
	f.AddInputField(l10n.Text("registry.path"), "", 0, nil, func(value string) { path = value })
	f.AddButton(l10n.Text("registry.import"), func() {
		u.work(l10n.Text("registry.import"), func(context.Context) error { return u.cfg.Registry.ImportFile(path) }, func(e error) {
			if e != nil {
				u.failed(e)
				return
			}
			u.cfg.Profiles = u.cfg.Registry.Current().Profiles(u.cfg.FlasherVersion, "", nil)
			u.registryStatus()
		})
	})
	f.AddButton(l10n.Text("ui.back"), u.registryStatus)
	u.root(f)
	u.app.SetFocus(f)
}
