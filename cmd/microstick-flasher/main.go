package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"microstick.dev/tools/internal/dev"
	"microstick.dev/tools/internal/flasher"
	"microstick.dev/tools/internal/flasherui"
	"microstick.dev/tools/internal/l10n"
)

var version = "development"

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "microstick-flasher:", l10n.Diagnostic(e.Error()))
		os.Exit(1)
	}
}
func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	f := flag.NewFlagSet("microstick-flasher", flag.ContinueOnError)
	repo := f.String("repository", ".", l10n.Text("cli.repository"))
	distribution := f.String("distribution", "", l10n.Text("cli.distribution"))
	showVersion := f.Bool("version", false, l10n.Text("cli.version"))
	operation := f.String("operation", "tui", l10n.Text("cli.operation"))
	image := f.String("stock-image", "", l10n.Text("cli.stock_image"))
	boot1 := f.String("boot1", "", l10n.Text("cli.boot1"))
	boot2 := f.String("boot2", "", l10n.Text("cli.boot2"))
	sim := f.Bool("simulation", false, l10n.Text("cli.simulation"))
	simID := f.String("sim-id", "", l10n.Text("cli.sim_id"))
	serial := f.String("edl-serial", "", l10n.Text("cli.edl_serial"))
	output := f.String("output", "", l10n.Text("cli.output"))
	input := f.String("input", "", l10n.Text("cli.input"))
	edl := f.Bool("edl", false, l10n.Text("cli.edl"))
	activeFile := f.String("active-dtb", "", "active device tree captured from the connected device")
	if e := f.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *showVersion {
		if version == "development" {
			fmt.Println("microstick development (compatibility " + flasher.EngineVersion + ")")
			return nil
		}
		fmt.Println("microstick " + version)
		return nil
	}
	var p dev.Paths
	var e error
	if *distribution != "" {
		p, e = dev.OpenDistribution(*distribution)
	} else {
		p, e = dev.OpenWorkspace(*repo)
	}
	if e != nil {
		return e
	}
	var profiles []flasher.HardwareProfile
	client, e := flasher.NewRegistryClient(p.Work)
	registryError := e
	clientVersion := version
	if clientVersion == "development" {
		clientVersion = flasher.EngineVersion
	}
	if client != nil {
		profiles = client.Current().Profiles(clientVersion, "", nil)
	} else {
		profiles = nil
	}
	if strings.HasPrefix(*operation, "registry-") {
		if registryError != nil {
			return registryError
		}
		switch *operation {
		case "registry-update":
			if e = client.Refresh(ctx); e != nil {
				return e
			}
		case "registry-import":
			if e = client.ImportFile(*input); e != nil {
				return e
			}
		case "registry-status":
		default:
			return errors.New("unknown registry operation")
		}
		fmt.Printf("%s sequence=%d published=%s sha256=%s\n", client.Current().Source(), client.Current().Sequence(), client.Current().Published(), client.Current().Digest())
		return nil
	}
	external := func(path string) (string, error) { return dev.ExternalPath(p.Repo, path) }
	if *operation == "bootstrap" {
		_, _, _, e = dev.FlasherBootstrap(p, *edl)
		if e == nil {
			if p.Distribution != "" {
				fmt.Println("PASS: bundled native helpers verified; no device accessed")
			} else {
				fmt.Println("PASS: native helpers compiled; no device accessed")
			}
		}
		return e
	}
	if *operation == "led-candidate" {
		if *input == "" || *output == "" {
			return errors.New("--input and --output required")
		}
		src, e := external(*input)
		if e != nil {
			return e
		}
		dst, e := external(*output)
		if e != nil {
			return e
		}
		if _, e = os.Lstat(dst); !os.IsNotExist(e) {
			return errors.New("select a fresh candidate output")
		}
		b, e := os.ReadFile(src)
		if e != nil {
			return e
		}
		b, e = flasher.BuildOwnLED17(b)
		if e != nil {
			return e
		}
		return os.WriteFile(dst, b, 0600)
	}
	if *operation == "dtbs" {
		if *input == "" || *output == "" {
			return errors.New("--input and --output required")
		}
		src, e := external(*input)
		if e != nil {
			return e
		}
		dst, e := external(*output)
		if e != nil {
			return e
		}
		b, e := os.ReadFile(src)
		if e != nil {
			return e
		}
		vs, e := flasher.DTBs(b, filepath.Base(src))
		if e != nil {
			return e
		}
		return flasher.SaveJSON(dst, vs)
	}
	readActiveFile := func() ([]byte, error) {
		if *activeFile == "" {
			return nil, nil
		}
		path, e := external(*activeFile)
		if e != nil {
			return nil, e
		}
		return os.ReadFile(path)
	}
	selected := dev.USBConnection{Path: *serial}
	open := func(ctx context.Context, progress flasher.Emit) (flasherui.Session, error) {
		if *image != "" {
			paths := map[string]string{}
			for area, path := range map[string]string{"user": *image, "boot1": *boot1, "boot2": *boot2} {
				if path == "" {
					continue
				}
				abs, e := external(path)
				if e != nil {
					return flasherui.Session{}, e
				}
				paths[area] = abs
			}
			id := *simID
			if *sim && id == "" {
				return flasherui.Session{}, errors.New("--sim-id required for writable file simulation")
			}
			if id == "" {
				id = "offline-input"
			}
			d, e := flasher.OpenFiles(paths, id, *sim)
			if e != nil {
				return flasherui.Session{}, e
			}
			mapper, _, _, e := dev.FlasherBootstrapContext(ctx, p, false)
			if e != nil {
				d.Close()
				return flasherui.Session{}, e
			}
			active, e := readActiveFile()
			if e != nil {
				d.Close()
				return flasherui.Session{}, e
			}
			return flasherui.Session{Device: d, Closer: d, Mapper: mapper, ActiveDTB: active}, nil
		}
		if *sim {
			return flasherui.Session{}, errors.New("--simulation requires --stock-image")
		}
		if selected.Path == "" || strings.HasPrefix(selected.Path, "@") {
			if progress != nil {
				progress(flasher.Progress{Stage: "discovery", Message: l10n.Text("ui.looking_for_usb_connections")})
			}
			connections, err := dev.USBConnections(ctx)
			if err != nil {
				return flasherui.Session{}, &flasherui.DeviceDiscoveryError{Cause: err}
			}
			if selected.Path == "" {
				if len(connections) == 0 {
					return flasherui.Session{}, &flasherui.ConnectionStep{Message: l10n.Text("ui.no_usb_connections")}
				}
				if len(connections) != 1 {
					return flasherui.Session{}, errors.New("Select a USB connection before opening the device")
				}
				selected = connections[0]
			} else {
				found := false
				for _, device := range connections {
					if device.Path == selected.Path {
						selected = device
						found = true
						break
					}
				}
				if !found {
					return flasherui.Session{}, &flasherui.ConnectionStep{Message: l10n.Text("ui.no_usb_connections")}
				}
			}
			*serial = selected.Path
		}
		if selected.Transport == "adb" {
			if e := selected.Ready(); e != nil {
				return flasherui.Session{}, &flasherui.ConnectionStep{Message: e.Error()}
			}
			if progress != nil {
				progress(flasher.Progress{Stage: "active-dtb", Message: l10n.Text("ui.reading_device_tree")})
			}
			live, err := dev.CaptureLiveTree(ctx, p, selected.Path, progress)
			if err != nil {
				return flasherui.Session{}, err
			}
			selected.Transport = "" // Re-enumerate this USB path on Retry.
			if live != nil {
				return flasherui.Session{}, &flasherui.ConnectionStep{Message: l10n.Text("ui.active_tree_switch_edl")}
			}
			return flasherui.Session{}, &flasherui.ConnectionStep{Message: l10n.Text("ui.no_usb_connections")}
		}
		if progress != nil {
			progress(flasher.Progress{Stage: "helpers", Message: l10n.Text("ui.preparing_connection_tools")})
		}
		mapper, helper, programmer, e := dev.FlasherBootstrapContext(ctx, p, true)
		if e != nil {
			return flasherui.Session{}, e
		}
		saved, e := dev.LoadLiveTree(p, *serial)
		if e != nil {
			return flasherui.Session{}, e
		}
		if progress != nil {
			progress(flasher.Progress{Stage: "edl", Message: l10n.Text("ui.connecting_and_identifying_the_device")})
		}
		opening, cancelOpening := context.WithCancel(ctx)
		timer := time.AfterFunc(20*time.Second, cancelOpening)
		defer timer.Stop()
		var d *flasher.EDLReader
		deadline := time.Now().Add(20 * time.Second)
		for {
			d, e = flasher.OpenEDL(opening, helper, programmer, *serial)
			if e == nil || time.Now().After(deadline) || opening.Err() != nil {
				break
			}
			if !flasher.RetryableEDL(e) && !strings.Contains(e.Error(), "device-not-found") && !strings.Contains(e.Error(), "не найдено") {
				break
			}
			select {
			case <-opening.Done():
				cancelOpening()
				return flasherui.Session{}, opening.Err()
			case <-time.After(time.Second):
			}
		}
		if e != nil {
			cancelOpening()
			return flasherui.Session{}, e
		}
		timer.Stop()
		var active []byte
		if saved != nil {
			active = saved.DTB
		}
		if *activeFile != "" {
			active, e = readActiveFile()
			if e != nil {
				d.Close()
				return flasherui.Session{}, e
			}
		}
		return flasherui.Session{Device: d, Closer: d, Mapper: mapper, ActiveDTB: active}, nil
	}
	if *operation == "tui" {
		cfg := flasherui.Config{Paths: p, Open: open, Profiles: profiles, Simulation: *sim, Registry: client, FlasherVersion: clientVersion, RegistryError: registryError}
		if *image == "" && *serial == "" {
			cfg.SelectDevice = func(device dev.USBConnection) { selected = device; *serial = device.Path }
			cfg.Devices = dev.USBConnections
		}

		return flasherui.New(cfg).Run()
	}
	if *operation == "probe" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s, e := open(ctx, nil)
		if e != nil {
			return e
		}
		defer s.Closer.Close()
		info, e := s.Device.Info(ctx)
		if e != nil {
			return e
		}
		protocol := "file"
		if d, ok := s.Device.(*flasher.EDLReader); ok {
			protocol = d.Protocol
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"protocol": protocol, "geometry": info.Geometry, "storage_written": false})
	}
	if *operation != "inspect" && *operation != "backup" {
		return errors.New("unknown operation")
	}
	if *output == "" {
		return errors.New("--output required")
	}
	out, e := external(*output)
	if e != nil {
		return e
	}
	var lastProgress time.Time
	progress := func(p flasher.Progress) {
		if time.Since(lastProgress) >= 200*time.Millisecond || p.Stage != "active-dtb" {
			fmt.Fprintln(os.Stderr, l10n.Diagnostic(p.Message))
			lastProgress = time.Now()
		}
	}
	session, e := open(ctx, progress)
	if e != nil {
		return e
	}
	defer session.Closer.Close()
	reportDir := out
	if *operation == "backup" {
		reportDir = filepath.Join(filepath.Dir(out), "board-reports")
	}
	result, e := flasher.InspectWithFallback(ctx, session.Device, session.ActiveDTB, func(r flasher.HardwareReport) (flasher.HardwareProfile, error) {
		return dev.DetectAutomaticProfile(p, r)
	}, func(ctx context.Context) ([]flasher.HardwareProfile, error) {
		if client != nil {
			if err := client.Refresh(ctx); err != nil {
				fmt.Fprintln(os.Stderr, l10n.Diagnostic(client.Warning()))
			}
			profiles = client.Current().Profiles(clientVersion, "", nil)
		} else if registryError != nil {
			fmt.Fprintln(os.Stderr, l10n.Diagnostic(registryError.Error()))
		}
		return profiles, ctx.Err()
	}, reportDir, progress)
	if result.Archive != "" {
		fmt.Println(l10n.Text("cli.support_report"), result.Archive)
	}
	if e != nil {
		return e
	}
	if *operation == "inspect" {
		b, _ := json.Marshal(result.Report.Resolution)
		fmt.Println(string(b))
		return nil
	}
	// CLI backup requires an explicit output path and displays the complete upper
	// bound. It cannot enable any storage writes or bypass the installation gate.
	ranges, e := flasher.Coverage(result.Report)
	if e != nil {
		return e
	}
	var bytes uint64
	for _, r := range ranges {
		bytes += r.Length
	}
	fmt.Printf("Backup upper bound: %d bytes; ext4 free blocks omitted after allocation/journal verification\n", bytes)
	_, e = flasher.CreateBackup(ctx, session.Device, result.Report, out, flasher.BackupOptions{Mapper: session.Mapper, AllowFull: true})
	if e == nil {
		fmt.Println("PASS: verified backup saved; no storage writes")
	}
	return e
}
