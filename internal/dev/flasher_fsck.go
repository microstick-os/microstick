package dev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Recover a staged copy, never the device or the original rollback backup.
// Preen replays an ordinary dirty journal and performs safe automatic fixes;
// uncorrectable corruption remains an error with a private diagnostic log.
func cleanWizardFilesystem(ctx context.Context, p Paths, image string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if !within(image, filepath.Join(p.Work, "buildroot-private")) {
		return errors.New("filesystem recovery must stay in private staging")
	}
	st, e := os.Lstat(image)
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return errors.New("filesystem recovery requires a staged regular file")
	}
	dir := filepath.Join(p.Work, "buildroot-private/flasher-journals")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	log, e := os.CreateTemp(dir, "filesystem-*.log")
	if e != nil {
		return e
	}
	defer log.Close()
	if e = log.Chmod(0600); e != nil {
		return e
	}
	cmd := exec.CommandContext(ctx, "e2fsck", "-p", image)
	cmd.Stdout = log
	cmd.Stderr = log
	e = cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil {
		exit, ok := e.(*exec.ExitError)
		if !ok || exit.ExitCode() < 0 || exit.ExitCode()&^3 != 0 {
			return fmt.Errorf("не удалось восстановить файловую систему временной копии; журнал: %s", log.Name())
		}
	}
	cmd = exec.CommandContext(ctx, "e2fsck", "-fn", image)
	cmd.Stdout = log
	cmd.Stderr = log
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("временная файловая система не прошла проверку; журнал: %s", log.Name())
	}
	return ctx.Err()
}
