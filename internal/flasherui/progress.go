package flasherui

import (
	"fmt"
	"microstick.dev/tools/internal/l10n"
	"time"

	"microstick.dev/tools/internal/flasher"
)

type rateSample struct {
	at   time.Time
	done uint64
}

func progressGroup(p flasher.Progress) string {
	if p.PlanTotalBytes > 0 {
		return "plan"
	}
	return p.Stage
}

// Coalesce range events without delaying the writer for terminal redraws.
// TextView's io.Writer is synchronized; all UI state is consumed on the UI loop.
func (u *UI) progress(p flasher.Progress) {
	p.Message = l10n.Diagnostic(p.Message)
	if p.Total > 0 {
		fmt.Fprintf(u.details, "%s %d/%d %s\n", p.Stage, p.Done, p.Total, p.Message)
	} else {
		fmt.Fprintf(u.details, "%s %s\n", p.Stage, p.Message)
	}
	u.progressMu.Lock()
	group := progressGroup(p)
	immediate := group != u.progressGroup || p.Stage == "complete"
	u.pendingProgress, u.progressGroup = &p, group
	u.progressMu.Unlock()
	if immediate {
		u.app.QueueUpdateDraw(func() {
			u.consumeProgress(time.Now())
			u.renderProgress()
		})
	}
}

func progressBytes(p flasher.Progress) (uint64, uint64) {
	if p.PlanTotalBytes > 0 {
		return p.PlanDoneBytes, p.PlanTotalBytes
	}
	if p.Unit == "bytes" {
		return p.Done, p.Total
	}
	return 0, 0
}

func (u *UI) consumeProgress(now time.Time) {
	u.progressMu.Lock()
	if u.pendingProgress != nil {
		u.lastProgress, u.pendingProgress = u.pendingProgress, nil
	}
	u.progressMu.Unlock()
	if u.lastProgress == nil {
		return
	}
	p := *u.lastProgress
	done, total := progressBytes(p)
	group := fmt.Sprintf("%s:%d", progressGroup(p), total)
	if total == 0 || group != u.rateGroup || len(u.rateSamples) > 0 && done < u.rateSamples[len(u.rateSamples)-1].done {
		u.rateSamples, u.rateGroup, u.stageRate = nil, group, 0
	}
	if total == 0 {
		return
	}
	u.rateSamples = append(u.rateSamples, rateSample{now, done})
	cutoff := now.Add(-2 * time.Second)
	for len(u.rateSamples) > 1 && !u.rateSamples[1].at.After(cutoff) {
		u.rateSamples = u.rateSamples[1:]
	}
	first := u.rateSamples[0]
	if elapsed := now.Sub(first.at).Seconds(); elapsed >= .25 {
		u.stageRate = float64(done-first.done) / elapsed
	}
}

func (u *UI) renderProgress() {
	_, _, width, _ := u.status.GetInnerRect()
	u.status.SetText(u.progressText(width))
}

func (u *UI) progressText(width int) string {
	title, step, volume := u.workTitle, l10n.Text("ui.preparing"), ""
	if p := u.lastProgress; p != nil {
		title = p.Message
		if p.PlanTotalBytes > 0 {
			title = l10n.Text("ui.writing_and_verifying")
			if u.workTitle == l10n.Text("ui.restoring_a_backup") {
				title = l10n.Text("ui.restoring_and_verifying")
			}
		}
		done, total := progressBytes(*p)
		if total > 0 {
			percent := min(100, done*100/total)
			if p.PlanTotalBytes > 0 && p.Stage != "complete" {
				percent = min(99, percent)
			}
			step = fmt.Sprintf(l10n.Text("ui.progress"), percent)
			volume = fmt.Sprintf(l10n.Text("ui.size_mib"), float64(done)/(1<<20), float64(total)/(1<<20))
		} else if p.Total > 0 {
			step = fmt.Sprintf(l10n.Text("ui.step_of"), p.Done, p.Total)
		}
	}
	rate := l10n.Text("ui.progress_rate_pending")
	if u.stageRate > 0 {
		rate = fmt.Sprintf(l10n.Text("ui.progress_rate"), u.stageRate/(1<<20))
	}
	footer := l10n.Text("ui.f2_details_ctrl_c_cancel")
	if u.cancelRequested {
		step = l10n.Text("ui.cancelling_at_a_safe_boundary")
		footer = l10n.Text("ui.f2_details")
	}
	// The narrow layout depends only on terminal width, never on byte values.
	if width > 0 && width < 34 {
		if p := u.lastProgress; p != nil {
			done, total := progressBytes(*p)
			if total > 0 {
				volume = fmt.Sprintf(l10n.Text("ui.progress_size_short"), float64(done)/(1<<20), float64(total)/(1<<20))
			}
		}
	}
	return fmt.Sprintf(l10n.Text("ui.time"), title, step, volume, rate, duration(time.Since(u.started)), footer)
}
