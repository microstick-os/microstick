package flasherui

import "microstick.dev/tools/internal/l10n"

// DeviceDiscoveryError keeps command diagnostics out of the user-facing text.
type DeviceDiscoveryError struct{ Cause error }

func (e *DeviceDiscoveryError) Error() string {
	return l10n.Text("ui.could_not_check_the_modem_connection_check_the_usb")
}

func (e *DeviceDiscoveryError) Unwrap() error { return e.Cause }

// ConnectionStep requests a transport transition without treating it as an
// installation failure or offering recovery of an unwritten device.
type ConnectionStep struct{ Message string }

func (e *ConnectionStep) Error() string { return e.Message }
