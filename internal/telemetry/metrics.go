package telemetry

import (
	"sync"
	"time"
)

// InMemoryCollector stores counters and duration aggregates.
type InMemoryCollector struct {
	mu        sync.RWMutex
	counters  map[string]uint64
	durations map[string]durationStat
}

type durationStat struct {
	Count uint64
	Sum   time.Duration
	Max   time.Duration
}

func NewInMemoryCollector() *InMemoryCollector {
	return &InMemoryCollector{
		counters:  map[string]uint64{},
		durations: map[string]durationStat{},
	}
}

func (c *InMemoryCollector) IncCounter(name string) {
	if name == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters[name]++
}

func (c *InMemoryCollector) ObserveDuration(name string, d time.Duration) {
	if name == "" || d < 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.durations[name]
	st.Count++
	st.Sum += d
	if d > st.Max {
		st.Max = d
	}
	c.durations[name] = st
}

func (c *InMemoryCollector) Counter(name string) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.counters[name]
}

func (c *InMemoryCollector) Duration(name string) (count uint64, sum time.Duration, max time.Duration) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st := c.durations[name]
	return st.Count, st.Sum, st.Max
}
