package batch

import (
	"context"
	"errors"
	"fmt"
	"github.com/kushwagle00/espfleet/internal/config"
	"sync/atomic"
	"testing"
	"time"
)

type runnerFunc func(context.Context, config.Job, func(Event)) (Outcome, error)

func (f runnerFunc) Run(c context.Context, j config.Job, e func(Event)) (Outcome, error) {
	return f(c, j, e)
}
func jobs(n int) []config.Job {
	out := make([]config.Job, n)
	for i := range out {
		out[i].Device.Name = fmt.Sprint(i)
	}
	return out
}
func TestMixedResults(t *testing.T) {
	r := runnerFunc(func(_ context.Context, j config.Job, _ func(Event)) (Outcome, error) {
		if j.Device.Name == "1" {
			return Outcome{}, errors.New("unavailable port")
		}
		return Outcome{Verified: true, ResetRequested: true}, nil
	})
	got := Run(context.Background(), jobs(3), 2, time.Second, r, nil)
	if got[0].Status != "success" || got[1].Status != "failed" || got[2].Status != "success" {
		t.Fatalf("%+v", got)
	}
	if got[0].ApplicationHealth != "not_checked" {
		t.Fatal("false health claim")
	}
}
func TestConcurrencyLimit(t *testing.T) {
	var active, peak atomic.Int32
	gate := make(chan struct{})
	started := make(chan struct{}, 8)
	r := runnerFunc(func(_ context.Context, _ config.Job, _ func(Event)) (Outcome, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		<-gate
		return Outcome{Verified: true}, nil
	})
	done := make(chan []Result, 1)
	go func() { done <- Run(context.Background(), jobs(8), 2, time.Second, r, nil) }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	if active.Load() != 2 {
		t.Fatal("expected two workers")
	}
	close(gate)
	got := <-done
	if len(got) != 8 || peak.Load() > 2 {
		t.Fatal("concurrency limit exceeded")
	}
}
func TestCancellationStopsQueuedJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	r := runnerFunc(func(c context.Context, _ config.Job, _ func(Event)) (Outcome, error) {
		calls.Add(1)
		cancel()
		<-c.Done()
		return Outcome{}, c.Err()
	})
	got := Run(ctx, jobs(4), 1, time.Second, r, nil)
	if calls.Load() != 1 {
		t.Fatalf("started %d jobs", calls.Load())
	}
	for _, r := range got {
		if r.Status != "cancelled" {
			t.Fatalf("%+v", got)
		}
	}
}
func TestTimeoutAndUnverified(t *testing.T) {
	r := runnerFunc(func(c context.Context, _ config.Job, _ func(Event)) (Outcome, error) {
		<-c.Done()
		return Outcome{}, c.Err()
	})
	got := Run(context.Background(), jobs(1), 1, 10*time.Millisecond, r, nil)
	if got[0].Status != "cancelled" {
		t.Fatal(got)
	}
	r = runnerFunc(func(context.Context, config.Job, func(Event)) (Outcome, error) { return Outcome{}, nil })
	got = Run(context.Background(), jobs(1), 1, time.Second, r, nil)
	if got[0].Status != "failed" {
		t.Fatal("unverified success accepted")
	}
}

func TestFirmwareErrorStopsRemainingDevices(t *testing.T) {
	var calls atomic.Int32
	r := runnerFunc(func(context.Context, config.Job, func(Event)) (Outcome, error) {
		calls.Add(1)
		return Outcome{}, &config.FirmwareError{Err: errors.New("invalid image")}
	})
	got := Run(context.Background(), jobs(4), 1, time.Second, r, nil)
	if calls.Load() != 1 || got[0].ErrorKind != "firmware" {
		t.Fatalf("%+v", got)
	}
	for _, r := range got[1:] {
		if r.Status != "skipped" || r.ErrorKind != "firmware" {
			t.Fatalf("%+v", got)
		}
	}
}

func TestFirmwareErrorLetsInFlightBoardFinish(t *testing.T) {
	started := make(chan struct{})
	failed := make(chan struct{})
	r := runnerFunc(func(_ context.Context, j config.Job, _ func(Event)) (Outcome, error) {
		if j.Device.Name == "0" {
			<-started
			return Outcome{}, &config.FirmwareError{Err: errors.New("invalid firmware")}
		}
		if j.Device.Name == "1" {
			close(started)
			<-failed
			return Outcome{Verified: true}, nil
		}
		t.Errorf("queued board %s started", j.Device.Name)
		return Outcome{}, nil
	})
	got := Run(context.Background(), jobs(4), 2, time.Second, r, func(e Event) {
		if e.Device == "0" && e.Stage == "failed" {
			close(failed)
		}
	})
	if got[0].Status != "failed" || got[1].Status != "success" || got[2].Status != "skipped" || got[3].Status != "skipped" {
		t.Fatalf("%+v", got)
	}
}
