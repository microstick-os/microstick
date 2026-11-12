# Build and test

## Prerequisites

Use Linux and Go 1.23 or newer. Install the tools for your task:

| Task | Tools and libraries |
| --- | --- |
| Build | C/C++ compiler, make, binutils, bash, patch, tar, gzip, bzip2, xz, perl, rsync, cpio, file, coreutils, findutils, wget or curl, git, pkg-config, OpenSSL, util-linux, kmod, e2fsprogs |
| Extraction and installer | mtools, device-tree-compiler, libusb, libxml2 and libext2fs development headers |
| Native tests | GLib/GIO, libcurl, OpenSSL, libip4tc, libmnl and libnftnl development headers; Chromium/chromedriver, iproute2, iptables, nftables |

Target tests use QEMU and the toolchain from the Buildroot workspace.

## Workspace

Clone the [source repository](https://github.com/microstick-os/microstick) and run
host commands from its root. Set an absolute workspace path outside the repository:

```sh
git clone https://github.com/microstick-os/microstick.git
cd microstick
export MICROSTICK_WORKSPACE="$(dirname "$PWD")/microstick-work"
export GOCACHE="$MICROSTICK_WORKSPACE/host-tools/microstick-go/build-cache"
export GOMODCACHE="$MICROSTICK_WORKSPACE/host-tools/microstick-go/modules"
export CGO_ENABLED=0
```

Keep downloads, build caches, hardware files, credentials and generated images in
this workspace. Do not move an existing toolchain cache; it contains absolute paths.
First-party tools use Go, shell and C.

## Extract device files

Use your own stock device or its original eMMC image:

```sh
./extract-blobs
# Alternative: read an existing stock image.
./extract-blobs --stock-image /absolute/path/to/emmc-user.bin
```

Before live extraction, put the stock device in EDL mode (`05c6:9008`).
The helper reads GPT, boot, modem and persist. It does not write storage or reset
the device.

The helper downloads and checks common inputs from the
[resource catalog](../boards/resources.json).
It extracts your MPSS, WCNSS and Wi-Fi calibration from stock.
Do not substitute another device's calibration or the factory MBA.

| Option | Purpose |
| --- | --- |
| `--edl-serial` | Select one of several EDL devices |
| `--programmer` | Use a local programmer with the required pinned hash |
| `--output` | Select a new absolute output directory |

Default output is `$MICROSTICK_WORKSPACE/hardware/current`.
Existing output directories are rejected.
Set `MICROSTICK_HARDWARE` to an absolute bundle path to test another reviewed bundle.
Unknown hardware requires a reviewed profile and original subsystem DTBs.

## Build and verification

```sh
./devtool bootstrap
sh buildroot-external/scripts/build.sh
./devtool verify
go vet ./...
go test -count=1 -v ./...
./check-public
```

Bootstrap downloads pinned Buildroot 2025.02.19. Make persistent changes in the
external defconfig, packages or overlay. Do not edit the generated `.config`.
After changing local C sources, rebuild the affected package before the image:

```sh
sh buildroot-external/scripts/build.sh microstick-core-rebuild
sh buildroot-external/scripts/build.sh
```

Output is `$MICROSTICK_WORKSPACE/artifacts/buildroot-msm8916`.
Keep a copy of previous results before rebuilding. The bundle contains private
calibration and credentials. Builds reuse `buildroot-private/credentials.json`.
Do not replace live passwords or saved networks with build templates.

`devtool verify` checks image integrity, pinned payloads, ELF dependencies,
filesystem consistency, credentials and target tools under QEMU.
`check-public` checks repository contents. To check bundle checksums without changes:

```sh
(cd "$MICROSTICK_WORKSPACE/artifacts/buildroot-msm8916" && sha256sum -c SHA256SUMS)
```

Tests use temporary files and disposable network namespaces. They do not need an
attached modem. Run socket and network tests where namespaces are permitted.

## Installation image preparation

```sh
./prepare-install \
  --images "$MICROSTICK_WORKSPACE/artifacts/buildroot-msm8916" \
  --installation "$MICROSTICK_WORKSPACE/buildroot-private/installations/new-install.json" \
  --output "$MICROSTICK_WORKSPACE/artifacts/installations/new-install"
```

Use a new installation record for each new installation. Reuse that record on
retry. A changed base image or TAC requires a new record.
The base image has no default IMEI. Preparation creates a separate image with one
installation default: TAC `35290611`, six random digits and a Luhn check digit.
Use `--tac` to select another eight-digit TAC. A valid check digit does not prove
uniqueness or operator acceptance. Reboot retains the installation default.

For device installation, backup and rollback, use the [installer](flasher.md).

## Release package

Use Linux x86_64 and verified build inputs. Select a new output directory outside
the repository:

```sh
./devtool package-release --version 1.0.0 \
  --output "$MICROSTICK_WORKSPACE/releases/1.0.0"
```

The command removes private data from the release images. It includes the host
tools and their libraries. It does not access a device or publish the release.


## Board support

`./devtool boards validate` checks the source descriptors and their resource
references without opening a device. `boards status`, `boards update` and
`boards import --input /absolute/path/index.json` manage authenticated snapshots.
