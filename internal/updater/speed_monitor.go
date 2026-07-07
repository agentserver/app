package updater

import (
	"context"
	"io"
	"sync/atomic"
	"time"
)

type speedSampleRecord struct {
	t     time.Time
	bytes int64
}

type speedMonitor struct {
	window   time.Duration
	minBPS   int64
	now      func() time.Time
	tick     <-chan time.Time
	cancel   context.CancelFunc
	onSample func(SpeedSample)

	bytes   atomic.Int64
	tripped atomic.Bool

	// startNS is nanoseconds since epoch (Unix) captured on the FIRST
	// non-zero Read via countingReader. Zero means "no bytes yet, do
	// not measure". This makes "elapsed" the time since transfer
	// actually began — not since run() started — so dial + TLS +
	// slow-start don't leak into the throughput calc.
	startNS atomic.Int64

	// samples is touched only inside run(); no lock needed.
	samples []speedSampleRecord
}

func newSpeedMonitor(policy SourcePolicy, cancel context.CancelFunc, onSample func(SpeedSample)) *speedMonitor {
	return &speedMonitor{
		window:   policy.SpeedWindow,
		minBPS:   policy.MinSpeedBytesPerSec,
		now:      time.Now,
		cancel:   cancel,
		onSample: onSample,
	}
}

// withClock injects a fake clock + tick channel for tests. Returns
// receiver so it chains after newSpeedMonitor.
func (m *speedMonitor) withClock(now func() time.Time, tick <-chan time.Time) *speedMonitor {
	m.now = now
	m.tick = tick
	return m
}

// wrap returns an io.Reader that counts bytes into m.bytes on every Read.
func (m *speedMonitor) wrap(r io.Reader) io.Reader {
	return &countingReader{r: r, m: m}
}

// Tripped reports whether the monitor cancelled its ctx due to slow speed.
func (m *speedMonitor) Tripped() bool { return m.tripped.Load() }

// run blocks until ctx is done. If no tick channel was injected, it
// creates a 1s ticker. Tests inject their own.
//
// Callers that don't want the per-download goroutine (compat mode
// where the monitor is disabled AND no progress reporter is wired)
// should skip newSpeedMonitor+run entirely. run() itself always
// executes the loop so onSample fires — the trip check inside
// recordTick is what short-circuits when the policy disables it.
func (m *speedMonitor) run(ctx context.Context) {
	tick := m.tick
	if tick == nil {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick:
			// Ticks that arrive before any byte is read are ignored —
			// dial + TLS handshake time doesn't count against the
			// window. countingReader.Read sets startNS on first byte.
			startNS := m.startNS.Load()
			if startNS == 0 {
				if m.onSample != nil {
					m.onSample(SpeedSample{})
				}
				continue
			}
			start := time.Unix(0, startNS)
			m.recordTick(now, start)
			if m.tripped.Load() {
				return
			}
		}
	}
}

func (m *speedMonitor) recordTick(now, start time.Time) {
	b := m.bytes.Load()
	m.samples = append(m.samples, speedSampleRecord{t: now, bytes: b})

	// Trim samples older than the window.
	cutoff := now.Add(-m.window)
	i := 0
	for i < len(m.samples) && m.samples[i].t.Before(cutoff) {
		i++
	}
	if i > 0 {
		m.samples = m.samples[i:]
	}

	elapsed := now.Sub(start)
	if m.onSample != nil {
		m.onSample(SpeedSample{
			Downloaded:  b,
			Elapsed:     elapsed,
			BytesPerSec: instantBPS(m.samples),
		})
	}

	// A zero threshold or zero window disables slow-download detection.
	// Compat mode (Service.Sources == nil) uses this to preserve today's
	// "download runs until socket timeout" behavior in existing tests.
	if m.minBPS <= 0 || m.window <= 0 {
		return
	}
	if elapsed < m.window {
		return
	}
	if trailingBPS(m.samples) < float64(m.minBPS) {
		m.tripped.Store(true)
		m.cancel()
	}
}

func instantBPS(samples []speedSampleRecord) float64 {
	if len(samples) < 2 {
		return 0
	}
	a := samples[0]
	b := samples[len(samples)-1]
	dt := b.t.Sub(a.t).Seconds()
	if dt <= 0 {
		return 0
	}
	return float64(b.bytes-a.bytes) / dt
}

func trailingBPS(samples []speedSampleRecord) float64 {
	return instantBPS(samples)
}

type countingReader struct {
	r io.Reader
	m *speedMonitor
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		if c.m.startNS.Load() == 0 {
			// CompareAndSwap ⇒ only the first non-zero read sets start.
			c.m.startNS.CompareAndSwap(0, c.m.now().UnixNano())
		}
		c.m.bytes.Add(int64(n))
	}
	return n, err
}
