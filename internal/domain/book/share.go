package book

// Throughput is the node's declared throughput cap (value per HOUR or DAY).
type Throughput struct {
	Value int
	// PerHour is true for a HOUR cap, false for a DAY cap.
	PerHour bool
}

// ThroughputOrders converts a cap into the orders a window of windowMinutes
// operating minutes can carry (rounded up). dayMinutes is the operating minutes
// of the cutoff's day, used by a DAY cap; 0 means the whole cap.
func ThroughputOrders(c Throughput, windowMinutes, dayMinutes int) int {
	if c.Value < 1 || windowMinutes < 1 {
		return 0
	}
	if c.PerHour {
		return ceilDiv(int64(c.Value)*int64(windowMinutes), 60)
	}
	if dayMinutes < 1 {
		return c.Value
	}
	n := ceilDiv(int64(c.Value)*int64(windowMinutes), int64(dayMinutes))
	return min(n, c.Value)
}

// Share is the book share of gap G9: min(CutoffSlot.maxOrders, throughputCap x
// window) x the proportional share of the policy, rounded up, at least 1 and at
// most MaxShare. maxOrders 0 means the slot sets none; throughput 0 means the
// cap gives no bound.
func Share(maxOrders, throughput, percent int) int {
	base := throughput
	if maxOrders > 0 {
		base = maxOrders
		if throughput > 0 {
			base = min(maxOrders, throughput)
		}
	}
	if base < 1 {
		return 1
	}
	return min(ceilDiv(int64(base)*int64(percent), 100), MaxShare)
}

func ceilDiv(a, b int64) int {
	return int((a + b - 1) / b)
}
