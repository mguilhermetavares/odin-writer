package server

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// ---------------------------------------------------------------------------
// Server tests — fake time via testing/synctest
//
// Why synctest?
//   The original tests used time.Sleep(100–150 ms) to wait for ticks, making
//   them flaky under load and slow.  With synctest the ticker uses a fake
//   clock: time only advances when every goroutine in the bubble is durably
//   blocked, so tests are deterministic and instant.
// ---------------------------------------------------------------------------

// TestServer_ImmediateTick verifies that the server fires one tick right after
// start, before the interval elapses.
func TestServer_ImmediateTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &countingSource{media: successMedia("vid-immediate")}
		srv := New(newRunner(t, src, &noopPublisher{}), time.Hour) // long interval

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { srv.Run(ctx); close(done) }()

		// Block until the server goroutine parks on its select (tick done).
		synctest.Wait()

		if got := src.calls.Load(); got != 1 {
			t.Errorf("want 1 call (immediate tick), got %d", got)
		}

		cancel()
		<-done
	})
}

// TestServer_IntervalTick verifies that advancing fake time by one interval
// causes exactly one additional tick.
func TestServer_IntervalTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &countingSource{media: successMedia("vid-interval")}
		interval := 5 * time.Minute
		srv := New(newRunner(t, src, &noopPublisher{}), interval)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { srv.Run(ctx); close(done) }()

		synctest.Wait() // after immediate tick (calls==1)

		time.Sleep(interval) // fake-advance to next ticker fire
		synctest.Wait()      // after second tick (calls==2)

		if got := src.calls.Load(); got != 2 {
			t.Errorf("want 2 calls after 1 interval, got %d", got)
		}

		cancel()
		<-done
	})
}

// TestServer_ThreeIntervalsExactCount verifies that N interval advances produce
// exactly 1+N ticks (immediate + one per interval).
func TestServer_ThreeIntervalsExactCount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &countingSource{media: successMedia("vid-three")}
		interval := 2 * time.Minute
		srv := New(newRunner(t, src, &noopPublisher{}), interval)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { srv.Run(ctx); close(done) }()

		synctest.Wait() // tick 1 (immediate)
		for range 3 {
			time.Sleep(interval)
			synctest.Wait() // ticks 2, 3, 4
		}

		if got := src.calls.Load(); got != 4 {
			t.Errorf("want 4 calls (1 immediate + 3 interval), got %d", got)
		}

		cancel()
		<-done
	})
}

// TestServer_StopsOnCancel verifies that the server exits after context
// cancellation without needing any real sleep.
func TestServer_StopsOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &countingSource{media: successMedia("vid-cancel")}
		srv := New(newRunner(t, src, &noopPublisher{}), time.Hour)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { srv.Run(ctx); close(done) }()

		synctest.Wait() // wait for immediate tick to complete
		cancel()

		select {
		case <-done:
			// Good — exited immediately after cancel.
		case <-time.After(time.Second): // fake 1 s timeout
			t.Fatal("server did not stop after context cancellation")
		}
	})
}

// TestServer_TickErrorDoesNotStopServer verifies that a pipeline error on a
// tick is logged and the server continues to fire on subsequent intervals.
func TestServer_TickErrorDoesNotStopServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &countingSource{err: fmt.Errorf("source unavailable")}
		interval := 3 * time.Minute
		srv := New(newRunner(t, src, &noopPublisher{}), interval)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { srv.Run(ctx); close(done) }()

		synctest.Wait() // first failing tick

		time.Sleep(interval)
		synctest.Wait() // second failing tick

		if got := src.calls.Load(); got != 2 {
			t.Errorf("want 2 calls despite errors, got %d", got)
		}

		cancel()
		<-done
	})
}

// TestServer_FakeTimeDoesNotAdvanceDuringTick verifies that fake time stays
// still while the tick itself is executing — it only advances while goroutines
// are blocked on select / timer.
func TestServer_FakeTimeDoesNotAdvanceDuringTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &countingSource{media: successMedia("vid-time")}
		interval := 10 * time.Minute
		srv := New(newRunner(t, src, &noopPublisher{}), interval)

		start := time.Now()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { srv.Run(ctx); close(done) }()

		synctest.Wait() // immediate tick done

		// The tick executed zero blocking time operations, so fake time must
		// not have advanced by a full interval.
		if elapsed := time.Since(start); elapsed >= interval {
			t.Errorf("fake time advanced during tick execution: elapsed=%v, interval=%v",
				elapsed, interval)
		}

		cancel()
		<-done
	})
}
