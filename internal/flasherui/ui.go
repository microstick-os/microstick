// Package flasherui displays engine state; compatibility, storage and backup
// policy live in flasher. It has no model-selection or GPIO-probing shortcuts.
package flasherui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"microstick.dev/tools/internal/l10n"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"microstick.dev/tools/internal/dev"
	"microstick.dev/tools/internal/flasher"
)

type Session struct {
	Device    flasher.Reader
	Closer    io.Closer
	Mapper    string
	ActiveDTB []byte
}
type Config struct {
	Registry       *flasher.RegistryClient
	FlasherVersion string
	RegistryError  error
	Paths          dev.Paths
	Open           func(context.Context, flasher.Emit) (Session, error)
	Profiles       []flasher.HardwareProfile
	Simulation     bool
	Devices        func(context.Context) ([]dev.USBConnection, error)
	SelectDevice   func(dev.USBConnection)
	CopyPath       func(context.Context, string) (string, error)
	OpenFolder     func(context.Context, string) error
}
type UI struct {
	body            tview.Primitive
	form            *tview.Form
	draftID         string
	app             *tview.Application
	cfg             Config
	pages           *tview.Pages
	status, details *tview.TextView
	cancel          context.CancelFunc
	sessionCancel   context.CancelFunc
	active          bool
	session         Session
	started         time.Time
	retry           func()
	deviceChecked   bool
	workID          uint64
	lastProgress    *flasher.Progress
	stageRate       float64
	workTitle       string
	cancelRequested bool
	progressMu      sync.Mutex
	pendingProgress *flasher.Progress
	progressGroup   string
	rateGroup       string
	rateSamples     []rateSample
}

func New(cfg Config) *UI {
	if cfg.CopyPath == nil {
		cfg.CopyPath = copyDesktopPath
	}
	if cfg.OpenFolder == nil {
		cfg.OpenFolder = openDesktopFolder
	}
	u := &UI{app: tview.NewApplication(), cfg: cfg, pages: tview.NewPages(), status: tview.NewTextView(), details: tview.NewTextView().SetMaxLines(2000)}
	u.status.SetBorder(true).SetTitle(" microstick ")
	u.details.SetBorder(true).SetTitle(l10n.Text("ui.details_esc_back"))
	u.app.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF2 {
			u.showDetails()
			return nil
		}
		if e.Key() == tcell.KeyCtrlC {
			if u.active {
				u.cancelRequested = true
				u.cancel()
				u.renderProgress()
			} else {
				u.closeSession()
				u.app.Stop()
			}
			return nil
		}
		return e
	})
	u.menu()
	return u
}
func (u *UI) Run() error               { return u.app.SetRoot(u.pages, true).EnableMouse(true).Run() }
func (u *UI) SetScreen(s tcell.Screen) { u.app.SetScreen(s) }
func (u *UI) closeSession() {
	if u.sessionCancel != nil {
		u.sessionCancel()
		u.sessionCancel = nil
	}
	if u.session.Closer != nil {
		u.session.Closer.Close()
	}
	u.session = Session{}
}

func (u *UI) open(ctx context.Context) (Session, error) {
	lifetime, cancel := context.WithCancel(context.Background())
	stop := context.AfterFunc(ctx, cancel)
	s, e := u.cfg.Open(lifetime, u.progress)
	stop()
	if e != nil || ctx.Err() != nil {
		cancel()
		if s.Closer != nil {
			s.Closer.Close()
		}
		if e == nil {
			e = ctx.Err()
		}
		return Session{}, e
	}
	u.sessionCancel = cancel
	return s, nil
}
func (u *UI) root(p tview.Primitive) *shell {
	u.pages.RemovePage("main")
	u.body = p
	layout := &shell{Box: tview.NewBox(), body: p, status: u.status, working: u.active}
	if u.active {
		layout.statusText = func(width, _ int) string { return u.progressText(width) }
	}
	u.pages.AddPage("main", layout, true, true)
	return layout
}
func (u *UI) menu() {
	u.closeSession()
	u.deviceChecked = false
	if u.cfg.SelectDevice != nil {
		u.cfg.SelectDevice(dev.USBConnection{})
	}
	u.status.SetText(l10n.Text("ui.installation_wizard_tab_enter_select_f2_details_ctrl_c"))
	list := tview.NewList().ShowSecondaryText(false)
	for i, name := range []string{l10n.Text("ui.menu_install"), l10n.Text("ui.menu_update"), l10n.Text("ui.menu_backups"), l10n.Text("ui.menu_rollback"), l10n.Text("ui.menu_inspect")} {
		action := i
		list.AddItem(name, []string{l10n.Text("ui.install_with_an_automatic_backup"), l10n.Text("ui.latest_release_keeping_settings_and_data"), l10n.Text("ui.save_the_os_data_and_critical_areas"), l10n.Text("ui.restore_your_own_saved_backup"), l10n.Text("ui.identify_the_board_and_prepare_a_support_report")}[i], 0, func() {
			if action == 2 {
				u.backupManager()
			} else {
				u.start(action)
			}
		})
	}
	if u.cfg.Registry != nil || u.cfg.RegistryError != nil {
		list.AddItem(l10n.Text("registry.menu"), "", 0, u.registryStatus)
	}
	list.SetBorder(true).SetTitle(l10n.Text("ui.main_menu_choose_an_action"))
	names := []string{l10n.Text("ui.menu_install"), l10n.Text("ui.menu_update"), l10n.Text("ui.menu_backups"), l10n.Text("ui.menu_rollback"), l10n.Text("ui.menu_inspect")}
	if u.cfg.Registry != nil {
		names = append(names, l10n.Text("registry.menu"))
	}
	highlight := func(index int) {
		for i, name := range names {
			prefix := "  "
			if i == index {
				prefix = "› "
			}
			list.SetItemText(i, prefix+name, "")
		}
	}
	list.SetChangedFunc(func(index int, _ string, _ string, _ rune) { highlight(index) })
	list.SetSelectedTextColor(tcell.NewHexColor(0xc1d660)).SetSelectedBackgroundColor(tcell.NewHexColor(0x292d32))
	highlight(0)
	u.root(list)
	u.app.SetFocus(list)
}
func (u *UI) modal(text string, buttons []string, done func(int)) {
	m := tview.NewModal().SetText(text).AddButtons(buttons).SetDoneFunc(func(i int, _ string) { u.pages.RemovePage("dialog"); done(i) })
	u.pages.AddPage("dialog", m, true, true)
	u.app.SetFocus(m)
}
func (u *UI) showDetails() {
	u.details.SetDoneFunc(func(tcell.Key) { u.pages.RemovePage("details") })
	u.pages.AddPage("details", u.details, true, true)
	u.app.SetFocus(u.details)
}
func (u *UI) work(title string, fn func(context.Context) error, next func(error)) {
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.active = true
	u.started = time.Now()
	u.workTitle = title
	u.cancelRequested = false
	u.workID++
	id := u.workID
	u.lastProgress = nil
	u.progressMu.Lock()
	u.pendingProgress, u.progressGroup = nil, ""
	u.progressMu.Unlock()
	u.rateSamples, u.rateGroup, u.stageRate = nil, "", 0
	u.renderProgress()
	u.root(tview.NewBox())
	finished := make(chan struct{})
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-finished:
				return
			case <-ticker.C:
				u.app.QueueUpdateDraw(func() {
					if u.active && u.workID == id {
						u.consumeProgress(time.Now())
						u.renderProgress()
					}
				})
			}
		}
	}()
	go func() {
		defer close(finished)
		e := fn(ctx)
		u.app.QueueUpdateDraw(func() {
			u.consumeProgress(time.Now())
			u.renderProgress()
			u.active = false
			cancel()
			u.root(tview.NewBox())
			if errors.Is(e, context.Canceled) || u.cancelRequested && e == nil {
				u.menu()
				return
			}
			next(e)
		})
	}()
}
func (u *UI) failed(e error) {
	var step *ConnectionStep
	if errors.As(e, &step) {
		u.modal(step.Message, []string{l10n.Text("ui.retry"), l10n.Text("ui.main_menu")}, func(i int) {
			if i == 0 && u.retry != nil {
				u.retry()
			} else {
				u.menu()
			}
		})
		return
	}
	if errors.Is(e, context.Canceled) {
		u.menu()
		return
	}
	msg := l10n.Diagnostic(e.Error())
	if d, ok := u.session.Device.(*flasher.EDLReader); ok {
		msg += l10n.Text("ui.private_diagnostics") + d.Diagnostic
	}
	fmt.Fprintln(u.details, msg)
	if cause := errors.Unwrap(e); cause != nil {
		fmt.Fprintln(u.details, l10n.Text("ui.technical_cause"), l10n.Diagnostic(cause.Error()))
	}
	u.modal(msg, []string{l10n.Text("ui.retry"), l10n.Text("ui.menu_rollback"), l10n.Text("ui.main_menu")}, func(i int) {
		if i == 0 && u.retry != nil {
			u.retry()
		} else if i == 1 {
			u.start(3)
		} else {
			u.menu()
		}
	})
}
func (u *UI) support(result flasher.Inspection, after func()) {
	u.supportAction(result, l10n.Text("ui.main_menu"), after)
}
func (u *UI) supportAction(result flasher.Inspection, label string, after func()) {
	message := l10n.Diagnostic(flasher.SupportMessage(result.Archive))
	if result.Report.Resolution.Profile != "" {
		reasons := make([]string, len(result.Report.Resolution.Reasons))
		for i, reason := range result.Report.Resolution.Reasons {
			reasons[i] = l10n.Diagnostic(reason)
		}
		message = l10n.Text("ui.board_identified") + result.Report.Resolution.Profile + l10n.Text("ui.installation_preparation_stopped") + strings.Join(reasons, "; ") + l10n.Text("ui.support_report") + result.Archive + l10n.Text("ui.attach_the_archive_to_an_issue_in_the_microstick")
	}
	text := tview.NewTextView().SetText(message).SetWordWrap(true)
	text.SetBorder(true).SetTitle(l10n.Text("ui.add_board_support"))
	feedback := tview.NewTextView().SetWordWrap(true).SetText(l10n.Text("ui.pgup_pgdn_scroll_text_esc_back_to_menu"))
	actions := tview.NewList().ShowSecondaryText(false)
	busy := false
	run := func(fn func(context.Context) (string, error)) {
		if busy {
			return
		}
		busy = true
		feedback.SetText(l10n.Text("ui.working"))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			message, err := fn(ctx)
			u.app.QueueUpdateDraw(func() {
				busy = false
				if err != nil {
					message = l10n.Diagnostic(err.Error()) + l10n.Text("ui.hold_shift_to_select_the_path_and_copy_it")
				}
				feedback.SetText(message)
			})
		}()
	}
	actions.AddItem(l10n.Text("ui.copy_path"), "", 0, func() { run(func(ctx context.Context) (string, error) { return u.cfg.CopyPath(ctx, result.Archive) }) })
	actions.AddItem(l10n.Text("ui.open_folder"), "", 0, func() {
		run(func(ctx context.Context) (string, error) {
			return l10n.Text("ui.folder_open_request_sent"), u.cfg.OpenFolder(ctx, filepath.Dir(result.Archive))
		})
	})
	actions.AddItem(label, "", 0, after)
	box := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(text, 0, 1, false).AddItem(feedback, 3, 0, false).AddItem(actions, 3, 0, true)
	box.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch e.Key() {
		case tcell.KeyEscape:
			after()
			return nil
		case tcell.KeyTab:
			actions.SetCurrentItem((actions.GetCurrentItem() + 1) % 3)
			return nil
		case tcell.KeyBacktab:
			actions.SetCurrentItem((actions.GetCurrentItem() + 2) % 3)
			return nil
		case tcell.KeyPgDn:
			row, col := text.GetScrollOffset()
			text.ScrollTo(row+6, col)
			return nil
		case tcell.KeyPgUp:
			row, col := text.GetScrollOffset()
			text.ScrollTo(max(0, row-6), col)
			return nil
		}
		return e
	})
	u.status.SetText(l10n.Text("ui.support_report_tab_enter_select_pgup_pgdn_text_esc"))
	layout := u.root(box)
	layout.wide, layout.full = true, true
	u.app.SetFocus(actions)
}
func (u *UI) start(action int) {
	u.closeSession()
	u.retry = func() { u.start(action) }
	if !u.deviceChecked && u.cfg.Devices != nil {
		var devices []dev.USBConnection
		u.work(l10n.Text("ui.looking_for_usb_connections"), func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var e error
			devices, e = u.cfg.Devices(ctx)
			if e == nil && len(devices) == 0 {
				e = &ConnectionStep{Message: l10n.Text("ui.no_usb_connections")}
			}
			return e
		}, func(e error) {
			if e != nil {
				u.failed(e)
				return
			}
			u.deviceChecked = true
			if len(devices) <= 1 {
				if len(devices) == 1 && u.cfg.SelectDevice != nil {
					u.cfg.SelectDevice(devices[0])
				}
				u.start(action)
				return
			}
			list := tview.NewList()
			list.SetBorder(true).SetTitle(l10n.Text("ui.select_a_usb_connection"))
			for _, device := range devices {
				device := device
				list.AddItem("USB "+device.Path+" ("+strings.ToUpper(device.Transport)+")", "", 0, func() {
					if u.cfg.SelectDevice != nil {
						u.cfg.SelectDevice(device)
					}
					u.start(action)
				})
			}
			list.AddItem(l10n.Text("ui.back"), "", 0, u.menu)
			u.root(list)
			u.app.SetFocus(list)
		})
		return
	}
	if action == 3 {
		u.restoreForm()
		return
	}
	var result flasher.Inspection
	var pending *dev.FlasherOperation
	mode := "install"
	if action == 1 {
		mode = "update"
	}
	u.work(l10n.Text("ui.connecting_and_identifying_the_device"), func(ctx context.Context) error {
		s, e := u.open(ctx)
		if e != nil {
			return e
		}
		u.session = s
		if action == 0 || action == 1 {
			pending, e = dev.FindFlasherOperation(ctx, u.cfg.Paths, s.Device, mode)
			if e != nil {
				return e
			}
			if pending != nil && pending.Prepared {
				result.Report = pending.Report
				return nil
			}
		}
		profiles := u.cfg.Profiles
		if u.cfg.Simulation {
			r, e := flasher.Inspect(ctx, s.Device, profiles, nil)
			if e != nil {
				return e
			}
			if r.Resolution.Status != "matched" {
				profiles = append(profiles, flasher.SimulatorProfile(r))
			}
		}
		var detect func(flasher.HardwareReport) (flasher.HardwareProfile, error)
		if !u.cfg.Simulation {
			detect = func(r flasher.HardwareReport) (flasher.HardwareProfile, error) {
				return dev.DetectAutomaticProfile(u.cfg.Paths, r)
			}
		}
		result, e = flasher.InspectWithFallback(ctx, s.Device, s.ActiveDTB, detect, func(ctx context.Context) ([]flasher.HardwareProfile, error) {
			if u.cfg.Registry != nil && action != 2 {
				u.progress(flasher.Progress{Stage: "registry", Message: l10n.Text("registry.refresh")})
				if err := u.cfg.Registry.Refresh(ctx); err != nil {
					fmt.Fprintln(u.details, l10n.Diagnostic(u.cfg.Registry.Warning()))
				}
				profiles = u.cfg.Registry.Current().Profiles(u.cfg.FlasherVersion, "", nil)
			} else if u.cfg.RegistryError != nil {
				fmt.Fprintln(u.details, l10n.Diagnostic(u.cfg.RegistryError.Error()))
			}
			u.cfg.Profiles = profiles
			return profiles, ctx.Err()
		}, filepath.Join(u.cfg.Paths.Work, "board-reports"), u.progress)
		if result.Profile != nil {
			kept := u.cfg.Profiles[:0]
			for _, p := range u.cfg.Profiles {
				if p.ID != result.Profile.ID {
					kept = append(kept, p)
				}
			}
			u.cfg.Profiles = append(kept, *result.Profile)
		}
		return e
	}, func(e error) {
		if e != nil {
			u.failed(e)
			return
		}
		if action == 2 {
			u.backupForm(result.Report, func(path string) {
				fmt.Fprintln(u.details, l10n.Text("ui.backup"), path)
				u.modal(l10n.Text("ui.backup_created"), []string{l10n.Text("ui.back_to_backups")}, func(int) { u.backupManager() })
			})
			return
		}
		if result.Archive != "" {
			u.support(result, u.menu)
			return
		}
		if result.Report.Resolution.Status != "matched" {
			u.modal(l10n.Diagnostic(strings.Join(result.Report.Resolution.Reasons, "\n")), []string{l10n.Text("ui.main_menu")}, func(int) { u.menu() })
			return
		}
		if action == 4 {
			b, _ := json.MarshalIndent(result.Report.Resolution, "", "  ")
			u.modal(string(b), []string{l10n.Text("ui.main_menu")}, func(int) { u.menu() })
			return
		}
		if pending != nil && pending.Prepared {
			u.runInstall(pending, dev.WizardOptions{})
			return
		}
		if action == 1 {
			u.beginInstall(result.Report, dev.WizardOptions{Update: true}, pending)
			return
		}
		u.installForm(result.Report, pending)
	})
}

func (u *UI) preparationSupport(op *dev.FlasherOperation, cause error) {
	var input *flasher.UnsupportedInput
	if !errors.As(cause, &input) || input.Code == "boot-firmware-security" {
		u.failed(cause)
		return
	}
	r := op.Report
	r.Resolution.Status = "unsupported"
	r.Resolution.Reasons = []string{cause.Error()}
	r.InputFailure = input.Code
	var archive string
	u.work(l10n.Text("ui.preparing_a_support_report"), func(ctx context.Context) error {
		snapshot, e := flasher.OpenSnapshot(u.session.Device, op.Backup)
		if e != nil {
			return e
		}
		if e = flasher.VerifyBackup(ctx, op.Backup, r.Device, u.session.Mapper); e != nil {
			return e
		}
		archive, e = flasher.ExportBoardReport(ctx, snapshot, r, filepath.Join(u.cfg.Paths.Work, "board-reports"), u.progress)
		return e
	}, func(e error) {
		if e != nil {
			u.failed(e)
			return
		}
		u.support(flasher.Inspection{Report: r, Archive: archive}, u.menu)
	})
}
func (u *UI) profile(r flasher.HardwareReport) (flasher.HardwareProfile, error) {
	for _, p := range u.cfg.Profiles {
		if p.ID == r.Resolution.Profile {
			return p, nil
		}
	}
	if u.cfg.Simulation && r.Resolution.Profile == "file-simulator" {
		return flasher.SimulatorProfile(r), nil
	}
	return flasher.HardwareProfile{}, flasher.ErrUnsupported
}
func (u *UI) backupForm(r flasher.HardwareReport, next func(string)) {
	out := filepath.Join(u.cfg.Paths.Work, "backups", "flasher-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	u.work(l10n.Text("ui.creating_a_backup"), func(ctx context.Context) error {
		return u.connectedOperation(ctx, &r.Device, func() error {
			_, e := flasher.CreateBackup(ctx, u.session.Device, r, out, flasher.BackupOptions{Mapper: u.session.Mapper, AllowFull: true, Emit: u.progress})
			return e
		})
	}, func(e error) {
		if e != nil {
			u.failed(e)
			return
		}
		next(out)
	})
}
func (u *UI) installForm(r flasher.HardwareReport, pending *dev.FlasherOperation) {
	f := tview.NewForm()
	u.form = f
	f.SetBorder(true).SetTitle(l10n.Text("ui.install_title"))
	if pending != nil && pending.InstallationID != "" {
		u.draftID = pending.InstallationID
	}
	if u.draftID == "" {
		var e error
		u.draftID, e = dev.NewInstallationUUID()
		if e != nil {
			u.failed(e)
			return
		}
	}
	f.AddInputField(l10n.Text("ui.wi_fi_network_name"), dev.DefaultSSID(u.draftID), 0, nil, nil)
	f.AddPasswordField(l10n.Text("ui.wi_fi_and_panel_password"), "", 0, '*', nil)
	f.AddPasswordField(l10n.Text("ui.root_ssh_password"), "", 0, '*', nil)
	f.AddCheckbox(l10n.Text("ui.show_passwords"), false, func(show bool) {
		mask := '*'
		if show {
			mask = 0
		}
		for _, i := range []int{1, 2} {
			f.GetFormItem(i).(*tview.InputField).SetMaskCharacter(mask)
		}
	})
	clean := false
	if r.Boot.Mode == "lk2nd-23.1" || hasRootFS(r) {
		f.AddCheckbox(l10n.Text("ui.reset_settings_plugins_and_their_data"), false, func(v bool) { clean = v })
		f.AddTextView(l10n.Text("ui.data"), l10n.Text("ui.settings_plugins_and_their_data_are_preserved_by_default"), 0, 2, true, false)
	} else {
		f.AddTextView(l10n.Text("ui.data"), l10n.Text("ui.microstick_will_be_installed_the_previous_os_data_will"), 0, 3, true, false)
	}
	f.AddTextView("", l10n.Text("ui.a_backup_will_be_created_automatically"), 0, 1, true, false)
	f.AddButton(l10n.Text("ui.button_install"), func() {
		get := func(i int) string { return f.GetFormItem(i).(*tview.InputField).GetText() }
		o := dev.WizardOptions{InstallationID: u.draftID, SSID: get(0), APPassword: get(1), RootPassword: get(2), Clean: clean}
		for _, p := range r.GPT.Partitions {
			if p.Name == "rootfs" || p.Name == "system" {
				o.RootPARTUUID = p.GUID
			}
		}
		if e := dev.ValidateWizardOptions(o); e != nil {
			index := 0
			var field *dev.FormError
			if errors.As(e, &field) {
				index = field.Field
			}
			u.modal(l10n.Diagnostic(e.Error()), []string{l10n.Text("ui.correct")}, func(int) { f.SetFocus(index); u.app.SetFocus(f) })
			return
		}
		for _, i := range []int{1, 2} {
			f.GetFormItem(i).(*tview.InputField).SetText("")
		}
		u.beginInstall(r, o, pending)
	})
	f.AddButton(l10n.Text("ui.back"), u.menu)
	f.SetCancelFunc(u.menu)
	u.root(f)
	u.app.SetFocus(f)
}
func (u *UI) credentials(r flasher.HardwareReport, _, _ string) { u.installForm(r, nil) }
func (u *UI) beginInstall(r flasher.HardwareReport, o dev.WizardOptions, pending *dev.FlasherOperation) {
	var op *dev.FlasherOperation
	u.work(l10n.Text("ui.checking_the_firmware_bundle"), func(ctx context.Context) error {
		release, e := dev.ReadFlasherRelease(u.cfg.Paths.Artifacts)
		if e != nil {
			return e
		}
		profile, e := u.profile(r)
		if e != nil {
			return e
		}
		if c := profile.WithFirmwareVersion(release.Version).Compatibility(); c.Status != "" {
			return errors.New(c.Message)
		}
		if o.Update && pending == nil {
			installed, e := dev.ProbeInstalledRelease(ctx, u.session.Device, r, u.session.Mapper)
			if e != nil {
				return e
			}
			if e = dev.CheckFlasherUpdate(installed, release); e != nil {
				return e
			}
		}
		mode := "install"
		if o.Update {
			mode = "update"
		}
		if pending != nil {
			if pending.Release.ID != release.ID {
				return errors.New(l10n.Text("ui.the_unfinished_operation_uses_a_different_bundle_use_the"))
			}
			op = pending
		} else {
			op = dev.NewFlasherOperation(u.cfg.Paths, r, release, mode)
			op.InstallationID = o.InstallationID
			op.SSID = o.SSID
		}
		return ctx.Err()
	}, func(e error) {
		if e != nil {
			u.failed(e)
			return
		}
		u.runInstall(op, o)
	})
}
func (u *UI) runInstall(op *dev.FlasherOperation, o dev.WizardOptions) {
	profile, e := u.profile(op.Report)
	if op.Automatic != nil {
		profile, e = flasher.AutomaticProfile(op.Report, *op.Automatic)
	} else if u.cfg.Registry != nil && op.Registry != nil {
		var snapshot *flasher.RegistrySnapshot
		if u.cfg.Registry != nil {
			snapshot = u.cfg.Registry.Current()
		}
		profile, e = dev.OperationProfile(op, snapshot, u.cfg.FlasherVersion)
	}
	if e != nil {
		u.failed(e)
		return
	}
	u.work(l10n.Text("ui.backup_prepare_write_verify"), func(ctx context.Context) error {
		return u.connectedOperation(ctx, &op.Report.Device, func() error {
			return dev.RunFlasherOperation(ctx, u.cfg.Paths, u.session.Device, u.session.Mapper, op, profile, o, u.progress)
		})
	}, func(e error) {
		if e != nil {
			if errors.Is(e, flasher.ErrUnsupported) {
				u.preparationSupport(op, e)
				return
			}
			u.failed(e)
			return
		}
		fmt.Fprintf(u.details, l10n.Text("ui.backup_journal"), op.Backup, op.Journal)
		u.draftID = ""
		u.status.SetText(l10n.Text("ui.installation_complete_progress_100") + duration(time.Since(u.started)))
		u.modal(l10n.Text("ui.installation_complete_to_start_leave_edl_mode_and_reconnect"), []string{l10n.Text("ui.main_menu")}, func(int) { u.menu() })
	})
}
func (u *UI) restoreForm() {
	var backups []dev.BackupSummary
	u.work(l10n.Text("ui.looking_for_your_own_backups"), func(ctx context.Context) error {
		s, e := u.open(ctx)
		if e != nil {
			return e
		}
		u.session = s
		paths, e := dev.FlasherBackups(ctx, u.cfg.Paths, s.Device)
		if e != nil {
			return e
		}
		for _, path := range paths {
			item, e := dev.SummarizeBackup(path)
			if e == nil {
				backups = append(backups, item)
			}
		}
		return nil
	}, func(e error) {
		if e != nil {
			u.failed(e)
			return
		}
		u.restoreList(backups)
	})
}

func (u *UI) restoreList(backups []dev.BackupSummary) {
	list := newBackupList(l10n.Text("ui.menu_rollback"))
	for _, item := range backups {
		item := item
		list.AddItem(backupLabel(item), l10n.Text("ui.for_the_connected_device"), 0, func() { u.restore(item.Path) })
	}
	list.AddItem(l10n.Text("ui.another_backup_directory"), l10n.Text("ui.device_ownership_is_checked_before_writing"), 0, u.restorePath)
	list.AddItem(l10n.Text("ui.back"), "", 0, u.menu)
	list.SetDoneFunc(u.menu)
	list.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if index < len(backups) {
			u.backupDetails(backups[index])
		}
	})
	if len(backups) > 0 {
		u.backupDetails(backups[0])
	}
	u.backupRoot(list, l10n.Text("ui.backups_for_the_connected_device_only"), len(backups) == 0)
	u.app.SetFocus(list)
}
func (u *UI) restorePath() {
	u.status.SetText(l10n.Text("ui.restore_your_own_backup_device_ownership_and_backup_integrity"))
	f := tview.NewForm()
	f.AddInputField(l10n.Text("ui.backup_directory"), "", 0, nil, nil)
	f.AddButton(l10n.Text("ui.restore"), func() {
		path, e := dev.ExternalPath(u.cfg.Paths.Repo, f.GetFormItem(0).(*tview.InputField).GetText())
		if e != nil {
			u.status.SetText(l10n.Diagnostic(e.Error()))
			return
		}
		u.restore(path)
	})
	f.AddButton(l10n.Text("ui.back"), u.menu)
	f.SetCancelFunc(u.menu)
	u.root(f)
	u.app.SetFocus(f)
}
func (u *UI) restore(path string) {
	journal := filepath.Join(u.cfg.Paths.Work, "buildroot-private/flasher-journals", time.Now().UTC().Format("20060102T150405.000000000Z")+".jsonl")
	u.work(l10n.Text("ui.restoring_a_backup"), func(ctx context.Context) error {
		return u.connectedOperation(ctx, nil, func() error {
			ctx = flasher.WithBackupCache(ctx)
			plan, e := flasher.BuildRestorePlan(ctx, u.session.Device, path, u.session.Mapper)
			if e != nil {
				return e
			}
			w, e := dev.FlasherWriter(u.session.Device, plan)
			if e != nil {
				return e
			}
			if e = flasher.Execute(ctx, w, plan, path, path, journal, flasher.Hash(plan), u.progress, u.session.Mapper); e != nil {
				return e
			}
			for _, mode := range []string{"install", "update"} {
				op, e := dev.FindFlasherOperation(ctx, u.cfg.Paths, u.session.Device, mode)
				if e != nil {
					return e
				}
				if op != nil {
					op.Complete = true
					if e = dev.SaveFlasherOperation(op); e != nil {
						return e
					}
				}
			}
			return nil
		})
	}, func(e error) {
		if e != nil {
			u.failed(e)
			return
		}
		fmt.Fprintln(u.details, l10n.Text("ui.journal"), journal)
		u.modal(l10n.Text("ui.backup_restored_reconnect_the_device_to_start_it"), []string{l10n.Text("ui.main_menu")}, func(int) { u.menu() })
	})
}

// Transport errors get two bounded reconnect attempts. Integrity/identity
// failures never enter this path. The operation remeasures partial write ranges.
func (u *UI) connectedOperation(ctx context.Context, expected *flasher.DeviceInfo, run func() error) error {
	var failure error
	for attempt := 0; attempt < 3; attempt++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		if attempt > 0 {
			u.progress(flasher.Progress{Stage: "reconnect", Done: uint64(attempt), Total: 2, Message: l10n.Text("ui.connection_lost_reconnecting_automatically")})
			u.closeSession()
			s, e := u.open(ctx)
			if e != nil {
				failure = e
				if !flasher.RetryableEDL(e) {
					return e
				}
				continue
			}
			u.session = s
			if expected != nil {
				if d, ok := s.Device.(*flasher.EDLReader); ok {
					e = d.BindBackup(ctx, *expected)
				} else {
					var actual flasher.DeviceInfo
					actual, e = s.Device.Info(ctx)
					if e == nil && !flasher.EqualDevice(actual, *expected) {
						e = errors.New(l10n.Text("ui.a_different_device_was_detected_after_reconnecting"))
					}
				}
				if e != nil {
					u.closeSession()
					failure = e
					if flasher.RetryableEDL(e) {
						continue
					}
					return e
				}
			}
		}
		failure = run()
		if failure == nil || !flasher.RetryableEDL(failure) || u.cfg.Simulation || u.cfg.Open == nil {
			return failure
		}
	}
	return failure
}

func hasRootFS(r flasher.HardwareReport) bool {
	for _, p := range r.GPT.Partitions {
		if p.Name == "rootfs" {
			return true
		}
	}
	return false
}
