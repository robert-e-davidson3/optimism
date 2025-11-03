package metrics

type TestMetrics struct {
	noopMetrics
	PendingBlocksBytesCurrent float64
	ChannelQueueLength        int
	pendingDABytes            float64
	AltDAState                int
	AltDAFailureCount         uint64
}

var _ Metricer = new(TestMetrics)

func (m *TestMetrics) RecordL2BlockInPendingQueue(rawSize, daSize uint64) {
	m.PendingBlocksBytesCurrent += float64(rawSize)
	m.pendingDABytes += float64(daSize)
}
func (m *TestMetrics) RecordL2BlockInChannel(rawSize, daSize uint64) {
	m.PendingBlocksBytesCurrent -= float64(rawSize)
	m.pendingDABytes -= float64(daSize)
}
func (m *TestMetrics) RecordChannelQueueLength(l int) {
	m.ChannelQueueLength = l
}
func (m *TestMetrics) PendingDABytes() float64 {
	return m.pendingDABytes
}
func (m *TestMetrics) ClearAllStateMetrics() {
	m.PendingBlocksBytesCurrent = 0
	m.ChannelQueueLength = 0
	m.pendingDABytes = 0
}

func (m *TestMetrics) RecordPendingBlockPruned(rawSize, daSize uint64) {
	m.PendingBlocksBytesCurrent -= float64(rawSize)
	m.pendingDABytes -= float64(daSize)
}

func (m *TestMetrics) RecordAltDAState(state int) {
	m.AltDAState = state
}

func (m *TestMetrics) RecordAltDAFailureCount(count uint64) {
	m.AltDAFailureCount = count
}
