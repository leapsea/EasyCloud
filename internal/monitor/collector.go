package monitor

import (
	"context"
	"log/slog"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

const SampleInterval = 5 * time.Second

// Retention returns how long raw and per-minute points are kept.
type Retention func(ctx context.Context) (raw, minute time.Duration)

type Collector struct {
	store     MetricStore
	dataRoot  string
	retention Retention
	publish   func(sample map[string]float64)
	log       *slog.Logger

	prevDisk   *disk.IOCountersStat
	prevNet    *net.IOCountersStat
	prevAt     time.Time
	proc       *process.Process
	lastRollup int64
}

func NewCollector(store MetricStore, dataRoot string, ret Retention, publish func(map[string]float64), log *slog.Logger) *Collector {
	p, _ := process.NewProcess(int32(os.Getpid()))
	return &Collector{store: store, dataRoot: dataRoot, retention: ret, publish: publish, log: log, proc: p}
}

func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(SampleInterval)
	defer t.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	c.prune(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			c.sample(ctx, now)
			c.rollup(now)
		case <-prune.C:
			c.prune(ctx)
		}
	}
}

func sumDisk(m map[string]disk.IOCountersStat) *disk.IOCountersStat {
	var s disk.IOCountersStat
	for _, v := range m {
		s.ReadBytes += v.ReadBytes
		s.WriteBytes += v.WriteBytes
	}
	return &s
}

func rate(cur, prev uint64, dt float64) float64 {
	if cur < prev || dt <= 0 {
		return 0 // counter reset
	}
	return float64(cur-prev) / dt
}

// Snapshot reads instantaneous values (also used by the system info endpoint).
func (c *Collector) sample(ctx context.Context, now time.Time) {
	v := map[MetricID]float64{}
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) > 0 {
		v[MetricCPU] = p[0]
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		v[MetricMemUsed] = float64(m.Used)
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		v[MetricLoad1] = l.Load1
	}
	if u, err := disk.UsageWithContext(ctx, c.dataRoot); err == nil {
		v[MetricDataUsed] = float64(u.Used)
	}
	if c.proc != nil {
		if mi, err := c.proc.MemoryInfoWithContext(ctx); err == nil {
			v[MetricProcRSS] = float64(mi.RSS)
		}
	}
	dt := now.Sub(c.prevAt).Seconds()
	if io, err := disk.IOCountersWithContext(ctx); err == nil {
		cur := sumDisk(io)
		if c.prevDisk != nil {
			v[MetricDiskRead] = rate(cur.ReadBytes, c.prevDisk.ReadBytes, dt)
			v[MetricDiskWrite] = rate(cur.WriteBytes, c.prevDisk.WriteBytes, dt)
		}
		c.prevDisk = cur
	}
	if n, err := net.IOCountersWithContext(ctx, false); err == nil && len(n) > 0 {
		cur := n[0]
		if c.prevNet != nil {
			v[MetricNetRx] = rate(cur.BytesRecv, c.prevNet.BytesRecv, dt)
			v[MetricNetTx] = rate(cur.BytesSent, c.prevNet.BytesSent, dt)
		}
		c.prevNet = &cur
	}
	c.prevAt = now
	if err := c.store.Append(now, v); err != nil {
		c.log.Warn("metrics append", "err", err)
	}
	if c.publish != nil {
		out := map[string]float64{"t": float64(now.Unix())}
		for name, id := range MetricNames {
			if x, ok := v[id]; ok {
				out[name] = math.Round(x*100) / 100
			}
		}
		c.publish(out)
	}
}

// rollup aggregates each finished minute of raw samples into one Minute point.
func (c *Collector) rollup(now time.Time) {
	minute := now.Truncate(time.Minute).Unix() - 60 // the last complete minute
	if minute <= c.lastRollup {
		return
	}
	from := c.lastRollup + 60
	if c.lastRollup == 0 || minute-from > 3600 {
		from = minute // after a restart, only the previous minute
	}
	for m := from; m <= minute; m += 60 {
		for _, id := range MetricNames {
			pts, err := c.store.Range(id, Raw, time.Unix(m, 0), time.Unix(m+59, 0))
			if err != nil || len(pts) == 0 {
				continue
			}
			if err := c.store.PutAggregate(id, Minute, Aggregate(m, pts)); err != nil {
				c.log.Warn("metrics rollup", "err", err)
			}
		}
	}
	c.lastRollup = minute
}

// Aggregate merges points into one {min,max,avg,count} point at time t.
func Aggregate(t int64, pts []Point) Point {
	a := Point{T: t, Min: math.Inf(1), Max: math.Inf(-1)}
	var sum float64
	for _, p := range pts {
		n := float64(max(p.Count, 1))
		sum += p.Avg * n
		a.Count += max(p.Count, 1)
		a.Min = math.Min(a.Min, p.Min)
		a.Max = math.Max(a.Max, p.Max)
	}
	a.Avg = sum / float64(a.Count)
	return a
}

func (c *Collector) prune(ctx context.Context) {
	raw, minute := c.retention(ctx)
	now := time.Now()
	if err := c.store.Prune(Raw, now.Add(-raw)); err != nil {
		c.log.Warn("metrics prune raw", "err", err)
	}
	if err := c.store.Prune(Minute, now.Add(-minute)); err != nil {
		c.log.Warn("metrics prune minute", "err", err)
	}
}

// Query picks a granularity for the range and buckets the result to at most maxPoints.
func Query(s MetricStore, id MetricID, from, to time.Time, maxPoints int) ([]Point, error) {
	g := Raw
	if to.Sub(from) > 6*time.Hour {
		g = Minute
	}
	pts, err := s.Range(id, g, from, to)
	if err != nil || len(pts) <= maxPoints {
		return pts, err
	}
	span := to.Unix() - from.Unix()
	bucket := (span + int64(maxPoints) - 1) / int64(maxPoints)
	var out []Point
	var cur []Point
	start := pts[0].T - (pts[0].T-from.Unix())%bucket
	for _, p := range pts {
		for p.T >= start+bucket {
			if len(cur) > 0 {
				out = append(out, Aggregate(start, cur))
				cur = cur[:0]
			}
			start += bucket
		}
		cur = append(cur, p)
	}
	if len(cur) > 0 {
		out = append(out, Aggregate(start, cur))
	}
	return out, nil
}

// HostInfo is static-ish information for the dashboard header.
type HostInfo struct {
	Hostname   string  `json:"hostname"`
	OS         string  `json:"os"`
	Platform   string  `json:"platform"`
	Arch       string  `json:"arch"`
	CPUs       int     `json:"cpus"`
	MemTotal   uint64  `json:"memTotal"`
	UptimeSec  uint64  `json:"uptimeSec"`
	DiskTotal  uint64  `json:"diskTotal"`
	DiskUsed   uint64  `json:"diskUsed"`
	DiskFree   uint64  `json:"diskFree"`
	DiskFS     string  `json:"diskFs"`
	Goroutines int     `json:"goroutines"`
	Load1      float64 `json:"load1"`
}

func (c *Collector) HostInfo(ctx context.Context) HostInfo {
	h := HostInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Goroutines: runtime.NumGoroutine()}
	h.Hostname, _ = os.Hostname()
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		h.MemTotal = m.Total
	}
	if u, err := disk.UsageWithContext(ctx, c.dataRoot); err == nil {
		h.DiskTotal, h.DiskUsed, h.DiskFree, h.DiskFS = u.Total, u.Used, u.Free, u.Fstype
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		h.Load1 = l.Load1
	}
	h.UptimeSec, h.Platform = hostUptime(ctx)
	return h
}
