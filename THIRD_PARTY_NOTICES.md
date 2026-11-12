# Licensing and third-party components

Original microstick code and documentation use the [MIT License](LICENSE),
except for files with other license notices and the exceptions below.
Third-party software, data and firmware retain their original licenses and
copyright notices. The MIT License does not relicense a complete firmware image
or grant rights to third-party binaries or trademarks.

## Source exceptions

- `buildroot-external/package/microstick-statistics/src/` uses
  [GPL-2.0-or-later](https://www.gnu.org/licenses/old-licenses/gpl-2.0.html).
- The rmtfs storage patch retains Luca Weiss's authorship and the
  [upstream BSD-3-Clause terms](https://github.com/linux-msm/rmtfs/blob/293ab8babb27ac0f24247bb101fed9420c629c29/LICENSE).

## Installer dependencies

Versions are pinned in `go.mod` and `go.sum`. Go and these modules are compiled
into the installer; their licenses also apply to its binary distribution.

| Component | License and upstream notice |
| --- | --- |
| Go runtime and standard library | [BSD-3-Clause; The Go Authors](https://go.dev/LICENSE) |
| tcell v2.8.1 | [Apache-2.0](https://github.com/gdamore/tcell/blob/v2.8.1/LICENSE) |
| encoding v1.0.1 | [Apache-2.0](https://github.com/gdamore/encoding/blob/v1.0.1/LICENSE) |
| tview v0.42.0 | [MIT; copyright 2018 Oliver Kuederle](https://github.com/rivo/tview/blob/v0.42.0/LICENSE.txt) |
| uniseg v0.4.7 | [MIT; copyright 2019 Oliver Kuederle](https://github.com/rivo/uniseg/blob/v0.4.7/LICENSE.txt) |
| go-colorful v1.2.0 | [MIT; copyright 2013 Lucas Beyer](https://github.com/lucasb-eyer/go-colorful/blob/v1.2.0/LICENSE) |
| go-runewidth v0.0.16 | [MIT; copyright 2016 Yasuhiro Matsumoto](https://github.com/mattn/go-runewidth/blob/v0.0.16/LICENSE) |
| golang.org/x/sys v0.29.0 | [BSD-3-Clause; The Go Authors](https://cs.opensource.google/go/x/sys/+/v0.29.0:LICENSE) |
| golang.org/x/term v0.28.0 | [BSD-3-Clause; The Go Authors](https://cs.opensource.google/go/x/term/+/v0.28.0:LICENSE) |
| golang.org/x/text v0.21.0 | [BSD-3-Clause; The Go Authors](https://cs.opensource.google/go/x/text/+/v0.21.0:LICENSE) |

Unicode-derived tables in terminal libraries retain the
[Unicode data license](https://www.unicode.org/license.txt).
The native EDL helper incorporates
[linux-msm/qdl](https://github.com/linux-msm/qdl/blob/c72e7d152acd605de19b64ad4a605ae5b58a7cda/LICENSE)
under BSD-3-Clause, copyright Linaro Ltd. and Bjorn Andersson.

## System components

The Buildroot system and bundled native tools contain independently licensed
software. Release `packages.json` records Buildroot package versions, declared
licenses and download locations; `licenses/` contains collected license texts.
That inventory does not identify every host library or vendor firmware license.

Principal components include Linux (GPL-2.0-only), BusyBox and iptables (GPL-2.0),
libnftnl (GPL-2.0-or-later), e2fsprogs (GPL-2.0 and library-specific terms),
mtools (GPL-3.0-or-later), libusb (LGPL-2.1-or-later), OpenSSL (Apache-2.0),
and the C/C++ runtimes under their respective licenses and runtime exceptions.
qrtr and rmtfs use BSD-3-Clause; wireless-regdb uses ISC. The mobile provider
database uses the upstream public-domain dedication.

MIT source linked with GPL libraries, including microstick-routerd with libip4tc
and microstick-networkd with libnftnl, must be distributed in accordance with
the applicable GPL terms for the combined executable.
[lk2nd 23.1](https://github.com/msm8916-mainline/lk2nd/tree/23.1) contains MIT,
BSD, GPL and other components; its source license notices apply individually.

## Hardware resources and redistribution

[boards/resources.json](boards/resources.json) identifies the OpenStick archives
supplying the kernel, DTB, modules, MBA and TZ/HYP. Qualcomm firmware and the EDL
programmer are subject to their original vendor terms. OpenStick refers to
[Qualcomm's firmware agreement](https://github.com/HandsomeMod/qcom-firmware/blob/main/LICENSE)
and states commercial-distribution restrictions for its images in its
[README](https://github.com/OpenStick/OpenStick). These statements do not change
the GPL terms of Linux or grant additional rights to vendor firmware.

Redistribution requires the applicable permissions and notices for each binary,
including any modifications. Files extracted from a device retain their original
terms. A download URL or checksum alone is not a redistribution license.
GPL/LGPL binary distribution also requires the corresponding source or another
source-delivery method allowed by that component's license. The source must match
the distributed version and include required configuration, patches and build
scripts; a general upstream link does not replace it.
