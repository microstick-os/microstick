# microstick Buildroot external tree

See the [project README](../README.md), [developer workflow](../docs/development.md)
and [device control and API](../docs/architecture.md).

The build requires an external `MICROSTICK_WORKSPACE` and a hash-verified hardware
bundle produced by the developer-only `extract-blobs` helper. No Qualcomm binary
firmware, device calibration, credentials or generated images belong in this tree.
