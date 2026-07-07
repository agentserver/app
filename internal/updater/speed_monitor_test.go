package updater

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTicker provides a hand-controlled tick channel. now() returns
// the FIXED origin passed to newFakeTicker — the monitor's start-time
// capture is deterministic regardless of when the monitor goroutine
// actually reads m.now() relative to test-driven ticks.
type fakeTicker struct {
	ch     chan time.Time
	origin time.Time
}

func newFakeTicker(start time.Time) *fakeTicker {
	return &fakeTicker{ch: make(chan time.Time, 1), origin: start}
}

func (ft *fakeTicker) send(t time.Time) {
	ft.ch <- t
}

func (ft *fakeTicker) now() time.Time {
	return ft.origin
}

// startMonitor wires an ack channel so tests can synchronize Read →
// Send → sample-recorded. Each recorded tick sends to ack; test blocks
// on <-ack after each send. Guarantees deterministic ordering.
func startMonitor(ctx context.Context, policy SourcePolicy, cancel context.CancelFunc, ft *fakeTicker) (*speedMonitor, <-chan struct{}, <-chan struct{}) {
	ack := make(chan struct{}, 32)
	m := newSpeedMonitor(policy, cancel, func(SpeedSample) { ack <- struct{}{} }).withClock(ft.now, ft.ch)
	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()
	return m, ack, done
}

// tick sends a tick and waits for the monitor's onSample ack (or ctx.Done
// if the tick tripped the monitor).
func tick(t *testing.T, ft *fakeTicker, ack <-chan struct{}, ctx context.Context, at time.Time) {
	t.Helper()
	ft.send(at)
	select {
	case <-ack:
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("no ack within 500ms for tick at %v", at)
	}
}

func TestSpeedMonitorNoCancelBeforeWindowFull(t *testing.T) {
	policy := SourcePolicy{SpeedWindow: 10 * time.Second, MinSpeedBytesPerSec: 100}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	m, ack, done := startMonitor(ctx, policy, cancel, ft)

	for i := 1; i <= 5; i++ {
		tick(t, ft, ack, ctx, start.Add(time.Duration(i)*time.Second))
	}
	if m.Tripped() {
		t.Fatal("monitor tripped before window full")
	}
	if ctx.Err() != nil {
		t.Fatal("ctx cancelled before window full")
	}
	cancel()
	<-done
}

func TestSpeedMonitorCancelsWhenTrailingAvgBelowThreshold(t *testing.T) {
	// 10 bytes per tick = 10 B/s; threshold 1000 B/s ⇒ trip once window full.
	policy := SourcePolicy{SpeedWindow: 3 * time.Second, MinSpeedBytesPerSec: 1_000}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	m, ack, done := startMonitor(ctx, policy, cancel, ft)

	r := m.wrap(strings.NewReader(strings.Repeat("x", 1000)))
	buf := make([]byte, 10)
	for i := 1; i <= 3; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			t.Fatal(err)
		}
		tick(t, ft, ack, ctx, start.Add(time.Duration(i)*time.Second))
	}

	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("monitor did not cancel ctx within 200ms")
	}
	if !m.Tripped() {
		t.Fatal("Tripped() must be true after cancel")
	}
	<-done
}

func TestSpeedMonitorDoesNotCancelWhenTrailingAvgAboveThreshold(t *testing.T) {
	// 1000 bytes per tick = 1000 B/s; threshold 100 B/s ⇒ never trip.
	policy := SourcePolicy{SpeedWindow: 2 * time.Second, MinSpeedBytesPerSec: 100}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	m, ack, done := startMonitor(ctx, policy, cancel, ft)

	r := m.wrap(strings.NewReader(strings.Repeat("x", 10_000)))
	buf := make([]byte, 1000)
	for i := 1; i <= 3; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			t.Fatal(err)
		}
		tick(t, ft, ack, ctx, start.Add(time.Duration(i)*time.Second))
	}

	if m.Tripped() {
		t.Fatal("monitor tripped despite high throughput")
	}
	cancel()
	<-done
}

func TestSpeedMonitorDisabledWhenMinBPSZero(t *testing.T) {
	// Compat-mode policy: MinSpeedBytesPerSec == 0 ⇒ monitor never cancels
	// even when throughput is zero for many windows.
	policy := SourcePolicy{SpeedWindow: 1 * time.Second, MinSpeedBytesPerSec: 0}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	m, ack, done := startMonitor(ctx, policy, cancel, ft)

	for i := 1; i <= 20; i++ {
		tick(t, ft, ack, ctx, start.Add(time.Duration(i)*time.Second))
	}
	if m.Tripped() {
		t.Fatal("disabled monitor must not trip")
	}
	if ctx.Err() != nil {
		t.Fatal("disabled monitor must not cancel ctx")
	}
	cancel()
	<-done
}

func TestSpeedMonitorEmitsOnSamplePerTick(t *testing.T) {
	policy := SourcePolicy{SpeedWindow: 10 * time.Second, MinSpeedBytesPerSec: 0}
	start := time.Unix(1_000_000, 0)
	ft := newFakeTicker(start)
	ctx, cancel := context.WithCancel(context.Background())
	var count atomic.Int64
	m := newSpeedMonitor(policy, cancel, func(SpeedSample) { count.Add(1) }).withClock(ft.now, ft.ch)

	done := make(chan struct{})
	go func() { m.run(ctx); close(done) }()

	for i := 1; i <= 4; i++ {
		ft.send(start.Add(time.Duration(i) * time.Second))
	}
	// Loose synchronization: give monitor time to consume all 4 ticks.
	for waited := 0; waited < 50 && count.Load() < 4; waited++ {
		time.Sleep(2 * time.Millisecond)
	}
	if got := count.Load(); got != 4 {
		t.Fatalf("onSample count = %d, want 4", got)
	}
	cancel()
	<-done
}
