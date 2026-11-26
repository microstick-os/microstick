# Installation and recovery

Set the [workspace](development.md#workspace). Run `./flasher` on Linux.
The installer derives a device profile from its active hardware description or
selects a matching catalog profile. It produces a diagnostic archive when required
hardware bindings or installation inputs cannot be determined.
Attach this report to an [issue](https://github.com/microstick-os/microstick/issues)
to request hardware support.

The archive contains sanitized DTBs, storage geometry and partition ranges.
`SOURCES.json` binds each exported tree to its original hashes, board/revision
and container selectors. `SUPPORT.json` records the original source-set,
compatibility and storage fingerprints used for registry review. Its
`dt_sources_complete` field covers source-set export integrity only; `missing`
lists incomplete or changed source observations.

Boot diagnostics include TZ/HYP hashes, ELF load maps, image-version identifiers
and the Secure Boot observation. Source-version names do not block replacement
with the pinned ARM64 pair. Firmware payloads and private device data are excluded.

The installer selects Russian or English from the system message locale;
other locales use English. Translations are embedded from `internal/l10n/locales/`.

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

- Inspect actual primary TZ/HYP ELF classes, load segments, addresses and partition capacities. Reject invalid headers or incompatible memory maps.
- Retain a compatible ELF64 pair. Bind preparation to its observed hashes. Replace a legacy, mixed or unrecognized pair together with the pinned ARM64 firmware.
- Require a live probe that confirms disabled Secure Boot before an upgrade. The pinned programmer reads four bytes at `0x58098`. Bit 5 in each byte must be clear, with a final ACK. Stored observations cannot replace this probe.
- Bind the plan to actual GPT addresses, security evidence, input hashes and the owner's backup. Preserve GPT, NV and calibration.
- Write occupied system/rootfs ranges first. If an upgrade is required, write primary TZ/HYP next. Write boot last. Do not substitute backup firmware slots.
- Once the firmware/boot group starts, complete it with readback. A failure records `recovery-required`; there is no automatic reset. Backup restore also completes its associated chain after writing starts.

TZ.BF.3.0/ELF64 can be retained. Other source versions use the replacement path.
Secure firmware load addresses are HYP `0x86400000` and TZ `0x86500000`.
HYP must declare the AArch64 machine type.
ELF64 TZ can retain `EM_ARM`; that field alone does not establish its ABI.
Rollback restores the owner's original raw TZ/HYP.

Inputs: [resource catalog](../boards/resources.json).
Protocol references: [Firehose](https://github.com/bkerler/edl/blob/master/edlclient/Library/firehose.py),
[SEC_BOOT mapping](https://github.com/bkerler/edl/blob/master/edlclient/Config/qualcomm_config.py).

### Device tree selection

On a running device the flasher reads `/sys/firmware/fdt`, with a fallback to
`/proc/device-tree` through ADB. The fallback needs neither root nor target-side
shell tools. ADB can be available through a local server or installed host tools.
USB discovery lists both ADB and EDL connections without reading the tree.
Tree collection displays node/property counts and has a two-minute total limit
and a ten-second response timeout. Ctrl+C cancels collection.
The tree stays in the private workspace. Switch to EDL on the same USB port when
prompted; the flasher does not reboot the device automatically.

A unique or hardware-equivalent tree in the boot image can be used directly in
EDL. Non-equivalent variants need a live tree or a matching catalog recipe.
`--active-dtb /absolute/path/to/active.dtb` also accepts a captured tree.

The MSM8916 mapping uses the Linux #17 kernel and modules, with the active RAM
banks, eMMC bus width and supply voltages, and GPIO LED/button bindings.
SIM controls retain their target mapping and defaults. A different component or
binding produces a report naming the missing inputs. Calibration and DSP files
come from the connected device.

Automatic profiling selects a profile from the component information first.
Catalog profiles provide fallback installation recipes and explicit hardware
adaptations when the component mapping lacks sufficient information.
Kernel, module and firmware downloads use the resource catalog.

## Board registry

Catalog entries describe supported boards and include the installation recipe.
Board profiles update independently of the application. **Board registry** shows
the active snapshot and offers refresh or import of a signed offline file.
An attempt that needs a catalog profile refreshes it and matches the device again.
An unavailable update retains the verified cache or bundled profiles. A recognized
board can require a newer flasher or firmware bundle; the installer displays its
requirements and the [release page](https://github.com/microstick-os/microstick/releases).
