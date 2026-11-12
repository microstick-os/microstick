package flasher

// planMeter measures payload work, independently of transport and range sizes.
// Resumed receipts are counted once; source scans do not count as payload work.
type planMeter struct {
	emitFn               Emit
	done, total, current uint64
}

func newPlanMeter(p InstallPlan, completed map[int]bool, emitFn Emit) *planMeter {
	m := &planMeter{emitFn: emitFn}
	for i, op := range p.Operations {
		m.total += 2 * op.Range.Length
		if completed[i] {
			m.done += 2 * op.Range.Length
		}
	}
	return m
}

func (m *planMeter) emit(p Progress) {
	if m.emitFn == nil {
		return
	}
	local := uint64(0)
	if p.Unit == "bytes" && p.Stage == "write" {
		local = min(p.Done, m.current)
	} else if p.Stage == "readback" {
		local = m.current + min(p.Done, m.current)
	}
	p.Version = Schema
	p.PlanDoneBytes, p.PlanTotalBytes = m.done+local, m.total
	p.Overall = min(99, int(p.PlanDoneBytes*100/max(1, m.total)))
	if p.Stage == "complete" {
		p.PlanDoneBytes, p.Overall = m.total, 100
	}
	m.emitFn(p)
}
