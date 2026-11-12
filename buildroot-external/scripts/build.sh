#!/bin/sh
set -eu
EXTERNAL=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PROJECT=${MICROSTICK_WORKSPACE:?Set MICROSTICK_WORKSPACE to an absolute external workspace}
case "$PROJECT" in /*) ;; *) echo 'MICROSTICK_WORKSPACE must be absolute' >&2; exit 1;; esac
"$EXTERNAL/../devtool" check-public
export SOURCE_DATE_EPOCH=1793577600
export BR2_DL_DIR="$PROJECT/buildroot-downloads/packages"
export PATH="$PROJECT/host-tools/bin:$PATH"
mkdir -p "$BR2_DL_DIR"
make -C "$PROJECT/buildroot" O="$PROJECT/buildroot-output" BR2_EXTERNAL="$EXTERNAL" msm8916_microstick_defconfig
exec make -C "$PROJECT/buildroot" O="$PROJECT/buildroot-output" BR2_EXTERNAL="$EXTERNAL" HOSTCFLAGS="-O2 -std=gnu17" HOSTCXXFLAGS="-O2 -std=gnu++17" -j"${JOBS:-16}" "$@"
