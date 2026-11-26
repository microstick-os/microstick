package flasher

import (
	"context"
	"errors"
)

type Inspection struct {
	Report  HardwareReport
	Archive string
	Profile *HardwareProfile
}

// InspectAndReport is shared by CLI and TUI: no interface can accidentally omit
// the automatic support archive when resolution is unsupported or conflicting.
func InspectAndReport(ctx context.Context, d Reader, profiles []HardwareProfile, reportsDir string, progress Emit) (Inspection, error) {
	return InspectWithActiveTree(ctx, d, profiles, nil, nil, reportsDir, progress)
}

func InspectWithActiveTree(ctx context.Context, d Reader, profiles []HardwareProfile, active []byte, detect func(HardwareReport) (HardwareProfile, error), reportsDir string, progress Emit) (Inspection, error) {
	return InspectWithFallback(ctx, d, active, detect, func(context.Context) ([]HardwareProfile, error) { return profiles, nil }, reportsDir, progress)
}

// InspectWithFallback derives a component profile before consulting the catalog.
// A fallback cannot bypass device integrity or boot security failures.
func InspectWithFallback(ctx context.Context, d Reader, active []byte, detect func(HardwareReport) (HardwareProfile, error), fallback func(context.Context) ([]HardwareProfile, error), reportsDir string, progress Emit) (Inspection, error) {
	r, e := Inspect(ctx, d, nil, progress)
	result := Inspection{Report: r}
	if e != nil {
		return result, e
	}
	if detect != nil {
		if len(active) == 0 {
			active, e = SourceDTB(ctx, d, r)
			if e != nil {
				return result, e
			}
			if len(active) > 0 {
				r.ActiveOrigin = "source"
			}
		} else {
			r.ActiveOrigin = "live"
		}
		r.ActiveDTB = append([]byte(nil), active...)
		if len(active) > 0 {
			e = BindActiveDTB(r, active)
			if e != nil {
				return Inspection{Report: r}, e
			}
			var profile HardwareProfile
			profile, e = detect(r)
			if e == nil {
				r.Resolution = Resolve(r, []HardwareProfile{profile})
				if r.Resolution.Status != "matched" {
					return Inspection{Report: r}, ErrUnsupported
				}
				r.BootFirmware, e = InspectBootFirmware(ctx, d, r, profile.BootFirmwarePolicy)
				if e != nil {
					return Inspection{Report: r}, e
				}
				return Inspection{Report: r, Profile: &profile}, nil
			}
		} else {
			e = &UnsupportedInput{Code: "active-dtb-source", Message: "Нет однозначного исходного или активного дерева устройства"}
		}
		var input *UnsupportedInput
		if !errors.As(e, &input) || input.Code == "boot-firmware-security" {
			return Inspection{Report: r}, e
		}
		r.InputFailure = input.Code
		r.Resolution.Reasons = append(r.Resolution.Reasons, input.Message)
	}
	if e = ctx.Err(); e != nil {
		return Inspection{Report: r}, e
	}
	var profiles []HardwareProfile
	if fallback != nil {
		profiles, e = fallback(ctx)
		if e != nil {
			return Inspection{Report: r}, e
		}
	}
	reasons := r.Resolution.Reasons
	r.Resolution = Resolve(r, profiles)
	if r.Resolution.Status == "matched" {
		for _, profile := range profiles {
			if profile.ID == r.Resolution.Profile && nativeIdentity(r.Device.Identity) {
				r.BootFirmware, e = InspectBootFirmware(ctx, d, r, profile.BootFirmwarePolicy)
				if e != nil {
					return Inspection{Report: r}, e
				}
			}
		}
		r.InputFailure = ""
		return Inspection{Report: r}, nil
	}
	result.Report = r
	if r.Resolution.Status == "flasher-update-required" || r.Resolution.Status == "firmware-update-required" {
		return result, nil
	}
	r.Resolution.Reasons = append(r.Resolution.Reasons, reasons...)
	result.Report = r
	archive, err := ExportBoardReport(ctx, d, r, reportsDir, progress)
	result.Archive = archive
	if err != nil {
		return result, errors.New("установка остановлена; не удалось сохранить board-report: " + err.Error())
	}
	return result, nil
}

func SupportMessage(path string) string {
	return "Плата неизвестна, профиль неоднозначен или обнаружен конфликт. Установка остановлена.\n\nСоздайте issue на https://github.com/microstick-os/microstick/issues и прикрепите этот архив для добавления поддержки:\n\n" + path + "\n\nАрхив содержит очищенный отчёт и исходные DTB, без пользовательских файлов и приватных идентификаторов."
}
