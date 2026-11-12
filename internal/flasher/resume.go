package flasher

import (
	"context"
	"errors"
)

// ResumePlan inspects every actual target range. Fully written ranges are
// verified again; partial ranges are rewritten from the saved checked payload.
func ResumePlan(ctx context.Context, d Reader, p InstallPlan, profile HardwareProfile) (InstallPlan, error) {
	if e := ValidatePlan(p); e != nil {
		return p, e
	}
	if p.Profile != profile.ID || p.Recipe != profile.Recipe || !profile.Qualified {
		return p, ErrUnsupported
	}
	if (p.Recipe == StockRecipe || nativeIdentity(p.Device.Identity)) && !canonicalNativeProfile(profile) {
		return p, ErrUnsupported
	}
	if p.Registry != nil && (profile.ProfileReference() == nil || *p.Registry != *profile.ProfileReference()) {
		return p, errors.New("saved plan requires its original registry snapshot")
	}
	if p.Registry == nil && profile.ProfileReference() != nil && !profile.LegacyCompatible() {
		return p, errors.New("saved plan lacks registry authorization; restore own backup")
	}
	if e := checkDevice(ctx, d, p.Device); e != nil {
		return p, e
	}
	if e := verifyProtected(ctx, d, p.Protected); e != nil {
		return p, e
	}
	p.qualification = &profile
	if e := verifyBootFirmwareExecution(ctx, d, p); e != nil {
		return p, e
	}
	if p.PayloadVersion == 1 {
		return p, nil
	}
	p.Operations = append([]Operation(nil), p.Operations...)
	for i := range p.Operations {
		h, e := HashRange(ctx, d, p.Operations[i].Range)
		if e != nil {
			return p, e
		}
		p.Operations[i].BeforeSHA256 = h
	}
	if e := ValidatePlan(p); e != nil {
		return p, errors.New("saved operation plan invalid")
	}
	return p, nil
}
