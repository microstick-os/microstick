package flasher

import (
	"encoding/json"
	"testing"
)

func assertPlanProgress(t *testing.T, plan InstallPlan, events []Progress, initial uint64) {
	t.Helper()
	var total uint64
	for _, op := range plan.Operations {
		total += 2 * op.Range.Length
	}
	if len(events) == 0 || events[0].PlanDoneBytes != initial {
		t.Fatalf("wrong resumed progress start: initial=%d events=%v", initial, events)
	}
	previous := initial
	for _, p := range events {
		if p.PlanTotalBytes != total || p.PlanDoneBytes < previous || p.PlanDoneBytes > total {
			t.Fatalf("invalid/nonmonotonic plan bytes: previous=%d total=%d event=%+v", previous, total, p)
		}
		if p.Stage != "complete" && p.Overall == 100 {
			t.Fatal("reported success before final completion")
		}
		previous = p.PlanDoneBytes
	}
	last := events[len(events)-1]
	if last.Stage != "complete" || last.PlanDoneBytes != total || last.Overall != 100 {
		t.Fatalf("missing final plan completion: %+v", last)
	}
}

func TestPlanMeterByteWeightingAndLegacySourceEvents(t *testing.T) {
	p := InstallPlan{Operations: []Operation{{Range: Range{Length: 512}}, {Range: Range{Length: 4096}}}}
	var events []Progress
	m := newPlanMeter(p, map[int]bool{0: true}, func(p Progress) { events = append(events, p) })
	m.current = 4096
	m.emit(Progress{Stage: "source", Unit: "bytes", Done: 4096, Total: 4096})
	m.emit(Progress{Stage: "write", Unit: "bytes", Done: 2048, Total: 4096})
	m.emit(Progress{Stage: "readback", Unit: "bytes", Done: 1024, Total: 4096})
	for i, want := range []uint64{1024, 3072, 6144} {
		if events[i].PlanDoneBytes != want || events[i].PlanTotalBytes != 9216 {
			t.Fatalf("event %d not weighted by bytes: %+v", i, events[i])
		}
	}
	// The additional JSON fields do not replace local range counters.
	raw, e := json.Marshal(events[2])
	var decoded map[string]any
	if e != nil || json.Unmarshal(raw, &decoded) != nil || decoded["done"] != float64(1024) || decoded["plan_done_bytes"] != float64(6144) || decoded["schema_version"] != float64(Schema) {
		t.Fatal("progress compatibility", string(raw), e)
	}
}
