// Package monitor samples host metrics and keeps their history in an embedded
// LSM key-value engine (Pebble) behind the MetricStore interface.
package monitor

import (
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/cockroachdb/pebble/v2"
)

// MetricID is stable on disk: never renumber.
type MetricID byte

const (
	MetricCPU       MetricID = 1 // percent
	MetricMemUsed   MetricID = 2 // bytes
	MetricLoad1     MetricID = 3
	MetricDiskRead  MetricID = 4 // bytes/s
	MetricDiskWrite MetricID = 5 // bytes/s
	MetricNetRx     MetricID = 6 // bytes/s
	MetricNetTx     MetricID = 7 // bytes/s
	MetricDataUsed  MetricID = 8 // bytes used on the data root filesystem
	MetricProcRSS   MetricID = 9 // bytes
)

var MetricNames = map[string]MetricID{
	"cpu": MetricCPU, "mem": MetricMemUsed, "load1": MetricLoad1,
	"disk_read": MetricDiskRead, "disk_write": MetricDiskWrite,
	"net_rx": MetricNetRx, "net_tx": MetricNetTx,
	"data_used": MetricDataUsed, "proc_rss": MetricProcRSS,
}

type Granularity byte

const (
	Raw    Granularity = 0 // one sample every interval (5s)
	Minute Granularity = 1 // {min,max,avg,count} per minute
)

type Point struct {
	T     int64   `json:"t"` // unix seconds
	Avg   float64 `json:"v"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Count uint32  `json:"n"` // samples averaged (lets clients fold live samples in)
}

// MetricStore is the persistence contract; Pebble is one implementation.
type MetricStore interface {
	Append(t time.Time, values map[MetricID]float64) error
	PutAggregate(id MetricID, g Granularity, p Point) error
	Range(id MetricID, g Granularity, from, to time.Time) ([]Point, error)
	Prune(g Granularity, before time.Time) error
	Close() error
}

// key = metric (1B) | granularity (1B) | unix seconds (8B, big endian).
// Big endian keeps one metric's points in time order, so a range read is a prefix scan.
func key(id MetricID, g Granularity, t int64) []byte {
	k := make([]byte, 10)
	k[0], k[1] = byte(id), byte(g)
	binary.BigEndian.PutUint64(k[2:], uint64(t))
	return k
}

type PebbleStore struct{ db *pebble.DB }

func OpenPebble(dir string) (*PebbleStore, error) {
	db, err := pebble.Open(dir, &pebble.Options{Logger: quietLogger{}})
	if err != nil {
		return nil, err
	}
	return &PebbleStore{db: db}, nil
}

func (s *PebbleStore) Close() error { return s.db.Close() }

func (s *PebbleStore) Append(t time.Time, values map[MetricID]float64) error {
	b := s.db.NewBatch()
	defer b.Close()
	for id, v := range values {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(v))
		if err := b.Set(key(id, Raw, t.Unix()), buf[:], nil); err != nil {
			return err
		}
	}
	return b.Commit(pebble.NoSync) // losing the last few samples on power loss is acceptable
}

func encodeAgg(p Point) []byte {
	buf := make([]byte, 28)
	binary.BigEndian.PutUint64(buf[0:], math.Float64bits(p.Min))
	binary.BigEndian.PutUint64(buf[8:], math.Float64bits(p.Max))
	binary.BigEndian.PutUint64(buf[16:], math.Float64bits(p.Avg))
	binary.BigEndian.PutUint32(buf[24:], p.Count)
	return buf
}

func (s *PebbleStore) PutAggregate(id MetricID, g Granularity, p Point) error {
	return s.db.Set(key(id, g, p.T), encodeAgg(p), pebble.NoSync)
}

func (s *PebbleStore) Range(id MetricID, g Granularity, from, to time.Time) ([]Point, error) {
	it, err := s.db.NewIter(&pebble.IterOptions{LowerBound: key(id, g, from.Unix()), UpperBound: key(id, g, to.Unix()+1)})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var out []Point
	for it.First(); it.Valid(); it.Next() {
		t := int64(binary.BigEndian.Uint64(it.Key()[2:]))
		v := it.Value()
		switch {
		case g == Raw && len(v) == 8:
			f := math.Float64frombits(binary.BigEndian.Uint64(v))
			out = append(out, Point{T: t, Avg: f, Min: f, Max: f, Count: 1})
		case len(v) == 28:
			out = append(out, Point{T: t,
				Min:   math.Float64frombits(binary.BigEndian.Uint64(v[0:])),
				Max:   math.Float64frombits(binary.BigEndian.Uint64(v[8:])),
				Avg:   math.Float64frombits(binary.BigEndian.Uint64(v[16:])),
				Count: binary.BigEndian.Uint32(v[24:])})
		}
	}
	return out, it.Error()
}

// Prune drops whole time ranges with DeleteRange; compaction reclaims the space.
func (s *PebbleStore) Prune(g Granularity, before time.Time) error {
	var errs []error
	for _, id := range MetricNames {
		errs = append(errs, s.db.DeleteRange(key(id, g, 0), key(id, g, before.Unix()), pebble.NoSync))
	}
	return errors.Join(errs...)
}

type quietLogger struct{}

func (quietLogger) Infof(string, ...any)  {}
func (quietLogger) Errorf(string, ...any) {}
func (quietLogger) Fatalf(format string, args ...any) {
	panic("pebble: fatal: " + format)
}
