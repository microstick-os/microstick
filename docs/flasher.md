# Installation and recovery

Set the [workspace](development.md#workspace). Run `./flasher` on Linux.
The installer detects reviewed hardware automatically. Unsupported or ambiguous
hardware produces a sanitized report before writing.
Attach this report to an [issue](https://github.com/microstick-os/microstick/issues)
to request hardware support.

The installer selects Russian or English from the system message locale
(`LC_ALL`, `LC_MESSAGES`, `LANG`, and the `LANGUAGE` preference list).
Other locales use English. Translations are embedded from `internal/l10n/locales/`.

## Actions and data

| Action | Result |
| --- | --- |
| Installation on stock | Save a backup; install rootfs in `system`; initialize `userdata` on first boot |
| Reinstallation | Preserve microstick data by default; the explicit reset option selects clean data |
| Update | Preserve networks, passwords, IMEI, keys and plugin data; replace unchanged default plugin code |
| Backups | Browse local copies without USB, or create a backup |
| Rollback | Restore a complete backup of the connected device |

New installations use `microstick-xxxxxxxx` as the default SSID. The suffix is
eight lowercase hexadecimal characters from the installation UUID. Retry retains
that identity. You can change the SSID in the installation form.

## Backups, retry and controls

Keep the automatic backup and operation records until you have checked the
installed device's operation.
Full and compact backups remain supported. Rollback checks device identity,
storage geometry and backup integrity. A model name or capacity match is insufficient.
Incomplete or foreign backups cannot be restored.

Retry checks the last completed range against device storage and rewrites an
unfinished range. Completed ranges retain their readback receipts.
Old pending plans without the current payload contract require own-backup recovery.
Do not edit saved plans to bypass validation.

| Key | Action |
| --- | --- |
| Arrows, Tab, Enter | Select and activate controls |
| Esc | Return to the previous screen |
| F2 | Show paths, logs and operation details |
| PgUp, PgDn | Scroll the diagnostic report |
| Ctrl+C | Cancel at a safe boundary |

Progress includes writing and readback. Use the report's copy-path or open-folder
action to inspect a failure. If clipboard access fails, copy the path manually.

## Boot firmware contract

Both recipes require `msm8916-arm64-v1`. Legacy ELF32 TZ/SCM cannot hand control
to the ARM64 kernel, even when lk2nd itself starts.

- Inspect actual primary TZ/HYP ELF classes, load segments, addresses and partition capacities. Reject missing, unknown or corrupt firmware.
- Retain a compatible ELF64 pair. Bind preparation to its observed hashes. Upgrade a legacy or mixed pair together.
- Require a live probe that confirms disabled Secure Boot before an upgrade. The pinned programmer reads four bytes at `0x58098`. Bit 5 in each byte must be clear, with a final ACK. Stored observations cannot replace this probe.
- Bind the plan to actual GPT addresses, security evidence, input hashes and the owner's backup. Preserve GPT, NV and calibration.
- Write occupied system/rootfs ranges first. If an upgrade is required, write primary TZ/HYP next. Write boot last. Do not substitute backup firmware slots.
- Once the firmware/boot group starts, complete it with readback. A failure records `recovery-required`; there is no automatic reset. Backup restore also completes its associated chain after writing starts.

The inspected families are TZ.BF.2.2/ELF32 and TZ.BF.3.0/ELF64.
The reviewed secure addresses are HYP `0x86400000` and TZ `0x86500000`.
HYP must declare the AArch64 machine type.
ELF64 TZ can retain `EM_ARM`; that field alone does not establish its ABI.
Rollback restores the owner's original raw TZ/HYP.

Inputs: [resource catalog](../buildroot-external/board/msm8916/resources.json).
Protocol references: [Firehose](https://github.com/bkerler/edl/blob/master/edlclient/Library/firehose.py),
[SEC_BOOT mapping](https://github.com/bkerler/edl/blob/master/edlclient/Config/qualcomm_config.py).

### Installed DTB

Do not import UF02 button GPIO23. Use calibration from the connected device.
Kernel, DTB, module and firmware hashes remain in the catalog.

| Candidate | Qualification limit |
| --- | --- |
| UFI001C | RGB22/21/20 differs; mainline DTS does not qualify the #17 bundle |
| UF02 | Similar RGB wiring; button GPIO23 differs |
| UZ801v3 | Upstream software support does not qualify this kernel/module/DSP bundle |
| SP970/UZ801 | Referenced upstream release reports a nonworking modem |
