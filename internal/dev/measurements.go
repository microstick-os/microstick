package dev

import (
	"microstick.dev/tools/internal/flasher"
	"time"
)

type StageMeasurement struct {
	LocalHashReadBytes uint64        `json:"local_hash_read_bytes"`
	Stage              string        `json:"stage"`
	Elapsed            time.Duration `json:"elapsed_ns"`
	ReadBytes          uint64        `json:"device_to_host_bytes"`
	WriteBytes         uint64        `json:"host_to_device_bytes"`
	TrafficMeasured    bool          `json:"traffic_measured"`
}

func measureStage(d flasher.Reader, stage string) func() StageMeasurement {
	started := time.Now()
	localStart := flasher.LocalHashBytes()
	native, ok := d.(interface{ Traffic() (uint64, uint64) })
	var r, w uint64
	if ok {
		r, w = native.Traffic()
	}
	return func() StageMeasurement {
		m := StageMeasurement{LocalHashReadBytes: flasher.LocalHashBytes() - localStart, Stage: stage, Elapsed: time.Since(started), TrafficMeasured: ok}
		if ok {
			read, written := native.Traffic()
			m.ReadBytes = read - r
			m.WriteBytes = written - w
		}
		return m
	}
}
