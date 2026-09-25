// Package batch coordinates independent device updates through an internal runner.
package batch

import (
	"context"
	"errors"
	"github.com/kushwagle00/espfleet/internal/config"
	"sync"
	"time"
)

type Event struct {
	Device  string `json:"device"`
	Stage   string `json:"stage"`
	Current int    `json:"current,omitempty"`
	Total   int    `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}
type Outcome struct {
	Verified       bool `json:"verified"`
	ResetRequested bool `json:"reset_requested"`
}
type Result struct {
	Device    string `json:"device"`
	Port      string `json:"port"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"`
	Outcome
	ApplicationHealth string `json:"application_health"`
	DurationMS        int64  `json:"duration_ms"`
}
type Runner interface {
	Run(context.Context, config.Job, func(Event)) (Outcome, error)
}

func Run(ctx context.Context, jobs []config.Job, parallel int, timeout time.Duration, runner Runner, emit func(Event)) []Result {
	results := make([]Result, len(jobs))
	for i, j := range jobs {
		results[i] = Result{Device: j.Device.Name, Port: j.Device.Port, Status: "cancelled", ApplicationHealth: "not_checked"}
	}
	var mu, eventMu sync.Mutex
	next := 0
	var firmwareErr error
	report := func(e Event) {
		if emit != nil {
			eventMu.Lock()
			defer eventMu.Unlock()
			emit(e)
		}
	}
	var wg sync.WaitGroup
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(jobs) {
		parallel = len(jobs)
	}
	for w := 0; w < parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if ctx.Err() != nil || firmwareErr != nil || next >= len(jobs) {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()
				jobCtx, cancel := context.WithTimeout(ctx, timeout)
				start := time.Now()
				out, err := runner.Run(jobCtx, jobs[i], report)
				var firmware *config.FirmwareError
				if errors.As(err, &firmware) {
					mu.Lock()
					firmwareErr = err
					mu.Unlock()
				}
				r := &results[i]
				r.Outcome = out
				r.DurationMS = time.Since(start).Milliseconds()
				switch {
				case jobCtx.Err() != nil:
					r.Status = "cancelled"
					r.Error = jobCtx.Err().Error()
				case err != nil:
					r.Status = "failed"
					r.Error = err.Error()
					r.ErrorKind = "device"
					if firmware != nil {
						r.ErrorKind = "firmware"
					}
				case !out.Verified:
					r.Status = "failed"
					r.Error = "transport did not confirm flash verification"
				default:
					r.Status = "success"
				}
				cancel()
				report(Event{Device: r.Device, Stage: r.Status, Message: r.Error})
			}
		}()
	}
	wg.Wait()
	if firmwareErr != nil {
		for i := next; i < len(results); i++ {
			results[i].Status = "skipped"
			results[i].ErrorKind = "firmware"
			results[i].Error = "batch stopped: " + firmwareErr.Error()
		}
	}
	return results
}
