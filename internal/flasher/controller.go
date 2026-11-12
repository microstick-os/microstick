package flasher

import (
	"context"
	"errors"
)

type Inspection struct {
	Report  HardwareReport
	Archive string
}

// InspectAndReport is shared by CLI and TUI: no interface can accidentally omit
// the automatic support archive when resolution is unsupported or conflicting.
func InspectAndReport(ctx context.Context, d Reader, profiles []HardwareProfile, reportsDir string, progress Emit) (Inspection, error) {
	r, e := Inspect(ctx, d, profiles, progress)
	result := Inspection{Report: r}
	if e != nil {
		var input *UnsupportedInput
		if errors.As(e, &input) {
			return result, e
		}
		if ValidateInfo(r.Device) != nil {
			return result, e
		}
		r.Issues = []string{"Не удалось достоверно разобрать GPT/исходные DTB"}
		r.Resolution = Resolution{Status: "conflict", Reasons: []string{"Исследование не завершено; запись запрещена"}}
		result.Report = r
	}
	if r.Resolution.Status == "flasher-update-required" || r.Resolution.Status == "firmware-update-required" || r.Resolution.Status == "blocked" {
		return result, e
	}
	if r.Resolution.Status != "matched" {
		archive, err := ExportBoardReport(ctx, d, r, reportsDir, progress)
		result.Archive = archive
		if err != nil {
			return result, errors.New("установка остановлена; не удалось сохранить board-report: " + err.Error())
		}
	}
	return result, e
}
func SupportMessage(path string) string {
	return "Плата неизвестна, профиль неоднозначен или обнаружен конфликт. Установка остановлена.\n\nСоздайте issue на https://github.com/microstick-os/microstick/issues и прикрепите этот архив для добавления поддержки:\n\n" + path + "\n\nАрхив содержит очищенный отчёт и исходные DTB/DTS, без пользовательских файлов и приватных идентификаторов."
}
