package transport

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/kushwagle00/espfleet/internal/batch"
	"github.com/kushwagle00/espfleet/internal/config"
	"tinygo.org/x/espflasher/pkg/espflasher"
)

type fakeFlasher struct {
	chip              espflasher.ChipType
	closed, reset     bool
	writes            int
	verifyErr         error
	mismatch, secured bool
	image             []byte
	opts              *espflasher.FlasherOptions
	resetFails        bool
}

func (f *fakeFlasher) ChipType() espflasher.ChipType { return f.chip }
func (f *fakeFlasher) Close() error                  { f.closed = true; return nil }
func (f *fakeFlasher) GetSecurityInfo() (*espflasher.SecurityInfo, error) {
	return &espflasher.SecurityInfo{ParsedFlags: espflasher.ParsedFlags{SecureBootEn: f.secured}}, nil
}
func (f *fakeFlasher) ReadRegister(uint32) (uint32, error) { return 0, nil }
func (f *fakeFlasher) FlashID() (uint8, uint16, error)     { return 0xef, 24, nil }
func (f *fakeFlasher) FlashImage(b []byte, _ uint32, p espflasher.ProgressFunc) error {
	f.writes++
	f.image = b
	p(len(b), len(b))
	return nil
}
func (f *fakeFlasher) GetFlashMD5(uint32, uint32, espflasher.ProgressFunc) (string, error) {
	if f.verifyErr != nil {
		return "", f.verifyErr
	}
	if f.mismatch {
		return "bad", nil
	}
	sum := md5.Sum(f.image)
	return hex.EncodeToString(sum[:]), nil
}
func (f *fakeFlasher) Reset() {
	f.reset = true
	if f.resetFails {
		f.opts.Logger.Logf("Warning: device may not have been reset: disconnected")
	}
}
func job() config.Job {
	return config.Job{Device: config.Device{Name: "cam", Port: "FAKE"}, Profile: config.Profile{Chip: "esp32s3", Reset: "default", Baud: 460800}, Images: []config.Binary{{File: "app.bin", Offset: 0x10000, Data: []byte("fixture")}}}
}
func TestFlashSafetyAndVerification(t *testing.T) {
	for _, name := range []string{"success", "wrong chip", "verify error", "digest mismatch", "secure boot", "reset failure"} {
		t.Run(name, func(t *testing.T) {
			f := &fakeFlasher{chip: espflasher.ChipESP32S3}
			switch name {
			case "wrong chip":
				f.chip = espflasher.ChipESP32C3
			case "verify error":
				f.verifyErr = errors.New("timeout")
			case "digest mismatch":
				f.mismatch = true
			case "secure boot":
				f.secured = true
			case "reset failure":
				f.resetFails = true
			}
			out, err := flash(job(), func(batch.Event) {}, func(_ string, opts *espflasher.FlasherOptions) (flasher, error) {
				f.opts = opts
				if opts.ChipType != espflasher.ChipAuto {
					t.Error("chip detection forced")
				}
				return f, nil
			})
			if !f.closed {
				t.Fatal("port not closed")
			}
			if name == "success" {
				if err != nil || !out.Verified || !f.reset {
					t.Fatalf("%+v %v", out, err)
				}
			} else if err == nil {
				t.Fatal("expected failure")
			}
			if (name == "wrong chip" || name == "secure boot") && f.writes != 0 {
				t.Fatal("unsafe write")
			}
			if (name == "verify error" || name == "digest mismatch") && (f.reset || out.Verified) {
				t.Fatal("verification failure reported as success")
			}
		})
	}
}
func TestPreservesHeaderCapacity(t *testing.T) {
	j := job()
	data := make([]byte, 48)
	data[0] = 0xe9
	data[1] = 1
	data[3] = 0x20
	data[12] = 9
	data[28] = 1
	data[47] = 0xef
	j.Images[0].Data = data
	f := &fakeFlasher{chip: espflasher.ChipESP32S3}
	_, err := flash(j, func(batch.Event) {}, func(_ string, o *espflasher.FlasherOptions) (flasher, error) { f.opts = o; return f, nil })
	if err != nil {
		t.Fatal(err)
	}
	if f.opts.FlashSize != "4MB" {
		t.Fatal("header overridden with physical capacity")
	}
}
func TestCapacityFailureBeforeWrite(t *testing.T) {
	j := job()
	j.Images[0].Offset = 1 << 24
	f := &fakeFlasher{chip: espflasher.ChipESP32S3}
	_, err := flash(j, func(batch.Event) {}, func(_ string, o *espflasher.FlasherOptions) (flasher, error) { f.opts = o; return f, nil })
	if err == nil || f.writes != 0 || !f.closed {
		t.Fatal("capacity guard failed")
	}
}

// The test executable acts as a deliberately stuck serial worker. No hardware is opened.
func TestMain(m *testing.M) {
	if os.Getenv("ESPFLEET_TEST_STUCK") == "1" && len(os.Args) == 2 && os.Args[1] == WorkerArgument {
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestProcessCancellation(t *testing.T) {
	t.Setenv("ESPFLEET_TEST_STUCK", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (Process{}).Run(ctx, job(), nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("child not terminated promptly")
	}
}
