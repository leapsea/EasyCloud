package monitor

import (
	"context"
	"testing"
	"time"
)

func TestPebbleStoreAndQuery(t *testing.T) {
	s, err := OpenPebble(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Unix(1_700_000_000, 0).Truncate(time.Minute)
	for i := 0; i < 24; i++ { // 2 minutes of 5s samples
		s.Append(base.Add(time.Duration(i)*5*time.Second), map[MetricID]float64{MetricCPU: float64(i), MetricMemUsed: 100})
	}
	pts, err := s.Range(MetricCPU, Raw, base, base.Add(time.Minute-time.Second))
	if err != nil || len(pts) != 12 || pts[0].Avg != 0 || pts[11].Avg != 11 {
		t.Fatalf("range %v %v", len(pts), err)
	}
	// Keys of other metrics must not bleed into the range.
	if mem, _ := s.Range(MetricMemUsed, Raw, base, base.Add(time.Hour)); len(mem) != 24 {
		t.Fatalf("mem points %d", len(mem))
	}

	c := &Collector{store: s}
	c.lastRollup = base.Unix() - 60
	c.rollup(base.Add(2*time.Minute + time.Second))
	agg, _ := s.Range(MetricCPU, Minute, base, base.Add(2*time.Minute))
	if len(agg) != 2 || agg[0].Min != 0 || agg[0].Max != 11 || agg[0].Avg != 5.5 || agg[0].Count != 12 || agg[1].Avg != 17.5 {
		t.Fatalf("rollup %+v", agg)
	}

	// Bucketing caps the number of points.
	q, _ := Query(s, MetricCPU, base, base.Add(2*time.Minute), 4)
	if len(q) > 4 || len(q) == 0 {
		t.Fatalf("bucketed %d", len(q))
	}

	// Prune removes old raw data but keeps aggregates.
	if err := s.Prune(Raw, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Range(MetricCPU, Raw, base, base.Add(time.Hour)); len(left) != 12 {
		t.Fatalf("after prune %d", len(left))
	}
	if agg, _ := s.Range(MetricCPU, Minute, base, base.Add(time.Hour)); len(agg) != 2 {
		t.Fatal("prune touched aggregates")
	}
}

func TestCollectorSamples(t *testing.T) {
	s, _ := OpenPebble(t.TempDir())
	defer s.Close()
	var got map[string]float64
	c := NewCollector(s, t.TempDir(), func(context.Context) (time.Duration, time.Duration) { return time.Hour, time.Hour },
		func(m map[string]float64) { got = m }, nil)
	now := time.Now()
	c.sample(context.Background(), now)
	c.sample(context.Background(), now.Add(5*time.Second))
	if got["mem"] <= 0 || got["data_used"] <= 0 {
		t.Fatalf("sample %v", got)
	}
	if pts, _ := s.Range(MetricMemUsed, Raw, now.Add(-time.Second), now.Add(time.Minute)); len(pts) != 2 {
		t.Fatalf("stored %d", len(pts))
	}
	if h := c.HostInfo(context.Background()); h.CPUs == 0 || h.DiskTotal == 0 {
		t.Fatalf("host %+v", h)
	}
}
