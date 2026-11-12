package flasherui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"microstick.dev/tools/internal/l10n"
	"os"
	"os/exec"
	"strings"
	"time"
)

func copyDesktopPath(ctx context.Context, path string) (string, error) {
	var commands [][]string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		commands = append(commands, []string{"wl-copy"})
	}
	if os.Getenv("DISPLAY") != "" {
		commands = append(commands, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"})
	}
	for _, argv := range commands {
		binary, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		cmd := exec.CommandContext(ctx, binary, argv[1:]...)
		cmd.Stdin = strings.NewReader(path)
		if cmd.Run() == nil {
			return l10n.Text("ui.path_copied"), nil
		}
		if ctx.Err() != nil {
			return "", errors.New(l10n.Text("ui.copying_the_path_timed_out"))
		}
	}
	if os.Getenv("TERM") == "dumb" || os.Getenv("TERM") == "" {
		return "", errors.New(l10n.Text("ui.clipboard_unavailable"))
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return "", errors.New(l10n.Text("ui.clipboard_unavailable"))
	}
	defer tty.Close()
	sequence := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(path)) + "\a"
	if os.Getenv("TMUX") != "" {
		sequence = "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	if _, err = fmt.Fprint(tty, sequence); err != nil {
		return "", errors.New(l10n.Text("ui.could_not_send_the_path_to_the_terminal_clipboard"))
	}
	return l10n.Text("ui.copy_request_sent_to_the_terminal_if_pasting_does"), nil
}

func openDesktopFolder(ctx context.Context, path string) error {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return errors.New(l10n.Text("ui.opening_folders_requires_a_graphical_desktop"))
	}
	for _, argv := range [][]string{{"xdg-open", path}, {"gio", "open", path}} {
		binary, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Some openers remain attached to the file manager. Reap them later,
		// without killing the user's newly opened window when this action ends.
		cmd := exec.Command(binary, argv[1:]...)
		if cmd.Start() != nil {
			continue
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err == nil {
				return nil
			}
		case <-time.After(350 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.New(l10n.Text("ui.could_not_open_the_folder_in_a_file_manager"))
}
