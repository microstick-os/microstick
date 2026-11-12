package flasherui

import (
	"context"
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"microstick.dev/tools/internal/dev"
	"microstick.dev/tools/internal/l10n"
)

func (u *UI) backupManager() {
	u.closeSession()
	items, e := dev.ListBackupSummaries(u.cfg.Paths)
	if e != nil {
		u.failed(e)
		return
	}
	list := newBackupList(l10n.Text("ui.manage_backups")).ShowSecondaryText(false)
	list.AddItem(l10n.Text("ui.create_backup"), l10n.Text("ui.save_data_from_the_connected_device"), 0, func() { u.start(2) })
	for _, item := range items {
		item := item
		label := item.DeviceLabel + " · " + backupLabel(item)
		if !item.Complete {
			label += l10n.Text("ui.incomplete")
		}
		list.AddItem(label, "", 0, func() {
			fmt.Fprintln(u.details, l10n.Text("ui.backup"), item.Path)
			if !item.Complete {
				u.modal(l10n.Text("ui.this_backup_is_incomplete_and_cannot_be_restored"), []string{l10n.Text("ui.back")}, func(int) { u.backupManager() })
				return
			}
			u.modal(fmt.Sprintf(l10n.Text("ui.backup_from_mib"), item.DeviceLabel, item.Created.Local().Format(l10n.Text("ui.date_format")), float64(item.Bytes)/(1<<20)), []string{l10n.Text("ui.restore"), l10n.Text("ui.back")}, func(i int) {
				if i != 0 {
					u.backupManager()
					return
				}
				u.work(l10n.Text("ui.connecting_to_the_device"), func(ctx context.Context) error {
					s, e := u.open(ctx)
					if e == nil {
						u.session = s
					}
					return e
				}, func(e error) {
					if e != nil {
						u.failed(e)
					} else {
						u.restore(item.Path)
					}
				})
			})
		})
	}
	list.AddItem(l10n.Text("ui.back"), "", 0, u.menu)
	list.SetDoneFunc(u.menu)
	list.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if index > 0 && index <= len(items) {
			u.backupDetails(items[index-1])
		}
	})
	u.backupRoot(list, l10n.Text("ui.all_local_backups"), false)
	u.app.SetFocus(list)
}

func (u *UI) backupRoot(list *tview.List, scope string, empty bool) {
	layout := u.root(list)
	layout.wide = true
	layout.statusText = func(width, height int) string {
		text := scope
		if empty {
			if width < 50 && height < 16 {
				text = l10n.Text("ui.backups_for_this_device_only")
			}
			text += l10n.Text("ui.no_matching_backups")
		}
		return text + l10n.Text("ui.enter_select_esc_back_f2")
	}
}

func newBackupList(title string) *tview.List {
	list := tview.NewList().ShowSecondaryText(true)
	list.SetSecondaryTextColor(tcell.NewHexColor(0xa0a9b5))
	list.SetSelectedTextColor(tcell.NewHexColor(0xc1d660)).SetSelectedBackgroundColor(tcell.NewHexColor(0x292d32))
	list.SetBorder(true).SetTitle(" " + title + " ")
	return list
}

func backupLabel(item dev.BackupSummary) string {
	return fmt.Sprintf(l10n.Text("ui.backup_label_format"), item.Created.Local().Format(l10n.Text("ui.date_format")), float64(item.Bytes)/(1<<20))
}

func (u *UI) backupDetails(item dev.BackupSummary) {
	fmt.Fprintf(u.details, l10n.Text("ui.backup_directory_complete"), item.DeviceLabel, backupLabel(item), item.Path, item.Complete)
	u.details.ScrollToEnd()
}
