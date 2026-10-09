// Package transport implements isolated serial workers. Each child handles one
// board, avoiding shared chip state in the upstream library across concurrent jobs.
package transport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5" // ESP ROM/stub verification protocol, not a security primitive.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"os/exec"
	"strings"

	"github.com/kushwagle00/espfleet/internal/batch"
	"github.com/kushwagle00/espfleet/internal/config"
	"tinygo.org/x/espflasher/pkg/espflasher"
)

const WorkerArgument = "__serial-worker"

type message struct {
	Event     *batch.Event   `json:"event,omitempty"`
	Outcome   *batch.Outcome `json:"outcome,omitempty"`
	Error     string         `json:"error,omitempty"`
	ErrorKind string         `json:"error_kind,omitempty"`
}

type Process struct{ Executable string }

func (p Process) Run(ctx context.Context, job config.Job, emit func(batch.Event)) (batch.Outcome, error) {
	var outcome batch.Outcome
	executable := p.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return outcome, err
		}
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return outcome, err
	}
	cmd := exec.CommandContext(ctx, executable, WorkerArgument)
	cmd.Env = append(os.Environ(), "ESPFLEET_WORKER=1")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return outcome, err
	}
	if err = cmd.Start(); err != nil {
		return outcome, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var workerErr error
	finished := false
	for scanner.Scan() {
		var m message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			workerErr = fmt.Errorf("invalid worker response: %w", err)
			_ = cmd.Process.Kill()
			break
		}
		if m.Event != nil && emit != nil {
			m.Event.Device = job.Device.Name
			emit(*m.Event)
		}
		if m.Outcome != nil {
			outcome = *m.Outcome
			finished = true
		}
		if m.Error != "" {
			workerErr = errors.New(m.Error)
			if m.ErrorKind == "firmware" {
				workerErr = &config.FirmwareError{Err: workerErr}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		workerErr = err
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return outcome, ctx.Err()
	}
	if workerErr != nil {
		return outcome, workerErr
	}
	if waitErr != nil {
		return outcome, fmt.Errorf("serial worker: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	if !finished {
		return outcome, errors.New("serial worker exited without a result")
	}
	return outcome, nil
}

// Worker runs only in a child process. Killing the process closes OS handles even
// when a driver or library call is blocked. Interrupted boards may need recovery.
func Worker(in io.Reader, out io.Writer) int {
	enc := json.NewEncoder(out)
	var job config.Job
	if err := json.NewDecoder(io.LimitReader(in, 96<<20)).Decode(&job); err != nil {
		_ = enc.Encode(message{Error: err.Error()})
		return 2
	}
	result, err := flash(job, func(e batch.Event) { _ = enc.Encode(message{Event: &e}) }, func(port string, opts *espflasher.FlasherOptions) (flasher, error) { return espflasher.New(port, opts) })
	m := message{Outcome: &result}
	if err != nil {
		m.Error = err.Error()
		var firmware *config.FirmwareError
		if errors.As(err, &firmware) {
			m.ErrorKind = "firmware"
			m.Error = firmware.Err.Error()
		}
	}
	if enc.Encode(m) != nil {
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

type flasher interface {
	ChipType() espflasher.ChipType
	Close() error
	GetSecurityInfo() (*espflasher.SecurityInfo, error)
	ReadRegister(uint32) (uint32, error)
	FlashID() (uint8, uint16, error)
	FlashImage([]byte, uint32, espflasher.ProgressFunc) error
	GetFlashMD5(uint32, uint32, espflasher.ProgressFunc) (string, error)
	Reset()
}
type opener func(string, *espflasher.FlasherOptions) (flasher, error)
type logger struct {
	emit         func(batch.Event)
	resetting    bool
	resetWarning string
}

func (l *logger) Logf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	if strings.HasPrefix(s, "Warning:") {
		l.emit(batch.Event{Stage: "warning", Message: s})
		if l.resetting {
			l.resetWarning = s
		}
	}
}

func flash(job config.Job, emit func(batch.Event), open opener) (out batch.Outcome, err error) {
	expected, ok := map[string]espflasher.ChipType{"esp32": espflasher.ChipESP32, "esp32c3": espflasher.ChipESP32C3, "esp32s3": espflasher.ChipESP32S3}[job.Profile.Chip]
	if !ok || len(job.Images) == 0 {
		return out, errors.New("invalid worker job")
	}
	for _, img := range job.Images {
		if e := config.ValidateFirmware(img.Data, job.Profile.Chip); e != nil {
			return out, &config.FirmwareError{Err: fmt.Errorf("%s: %w", img.File, e)}
		}
	}
	opts := espflasher.DefaultOptions()
	opts.ChipType = espflasher.ChipAuto // Never force the expected chip: detect and compare.
	opts.ConnectAttempts = 1
	opts.FlashBaudRate = job.Profile.Baud
	opts.FlashSize = "4MB" // Explicit value disables upstream automatic image-header edits.
	switch job.Profile.Reset {
	case "default":
		opts.ResetMode = espflasher.ResetDefault
	case "usb-jtag":
		opts.ResetMode = espflasher.ResetUSBJTAG
	case "no-reset":
		opts.ResetMode = espflasher.ResetNoReset
	default:
		return out, errors.New("invalid reset mode")
	}
	log := &logger{emit: emit}
	opts.Logger = log
	opts.ConnectStatus = func(phase espflasher.ConnectPhase, attempt, max int, msg string) {
		emit(batch.Event{Stage: "connecting", Message: string(phase) + ": " + msg})
	}
	emit(batch.Event{Stage: "connecting"})
	f, err := open(job.Device.Port, opts)
	if err != nil {
		return out, err
	}
	defer func() {
		if e := f.Close(); err == nil && e != nil {
			err = fmt.Errorf("close serial port: %w", e)
		}
	}()
	if f.ChipType() != expected {
		return out, fmt.Errorf("chip mismatch: expected %s, detected %s", expected, f.ChipType())
	}
	if err = checkSecurity(f, expected); err != nil {
		return out, err
	}
	_, id, err := f.FlashID()
	if err != nil {
		return out, fmt.Errorf("read flash capacity: %w", err)
	}
	capacityCode := uint8(id)
	if capacityCode < 20 || capacityCode > 25 {
		return out, fmt.Errorf("unsupported flash capacity code 0x%x", capacityCode)
	}
	capacity := uint64(1) << capacityCode
	sizes := make([]string, len(job.Images))
	for i, img := range job.Images {
		if len(img.Data) == 0 || uint64(img.Offset)+uint64(len(img.Data)) > capacity {
			return out, fmt.Errorf("%s exceeds detected flash capacity", img.File)
		}
		size, e := config.HeaderFlashSize(img.Data, job.Profile.Chip)
		if e != nil {
			return out, e
		}
		if size == "" {
			size = fmt.Sprintf("%dMB", capacity>>20)
		} else {
			headerCapacity := uint64(1) << (20 + (img.Data[3] >> 4))
			if headerCapacity > capacity || uint64(img.Offset)+uint64(len(img.Data)) > headerCapacity {
				return out, fmt.Errorf("%s: firmware header capacity conflicts with image range or physical flash", img.File)
			}
		}
		sizes[i] = size
	}
	for i, img := range job.Images {
		opts.FlashSize = sizes[i] // Keep the binary's own size nibble, including merged images.
		emit(batch.Event{Stage: "writing", Message: fmt.Sprintf("%s at 0x%x", img.File, img.Offset)})
		if err = f.FlashImage(img.Data, img.Offset, func(n, total int) { emit(batch.Event{Stage: "writing", Current: n, Total: total}) }); err != nil {
			return out, err
		}
	}
	// Strict second pass: upstream verification can suppress command errors.
	// Compare original bytes (excluding library alignment padding), failing closed.
	for _, img := range job.Images {
		emit(batch.Event{Stage: "verifying", Message: img.File})
		actual, e := f.GetFlashMD5(img.Offset, uint32(len(img.Data)), nil)
		if e != nil {
			return out, fmt.Errorf("verify %s: %w", img.File, e)
		}
		sum := md5.Sum(img.Data)
		if !strings.EqualFold(actual, hex.EncodeToString(sum[:])) {
			return out, fmt.Errorf("verify %s: flash digest mismatch", img.File)
		}
	}
	out.Verified = true
	emit(batch.Event{Stage: "resetting"})
	out.ResetRequested = true
	log.resetting = true
	f.Reset()
	if log.resetWarning != "" {
		return out, errors.New(log.resetWarning)
	}
	return out, nil
}

func checkSecurity(f flasher, chip espflasher.ChipType) error {
	if chip == espflasher.ChipESP32 {
		// ESP32 eFuse block 0: FLASH_CRYPT_CNT [26:20], ABS_DONE [5:4].
		// Register definitions: Espressif ESP32 TRM / esptool targets/esp32.py.
		crypt, err := f.ReadRegister(0x3ff5a000)
		if err != nil {
			return fmt.Errorf("read encryption state: %w", err)
		}
		boot, err := f.ReadRegister(0x3ff5a018)
		if err != nil {
			return fmt.Errorf("read secure boot state: %w", err)
		}
		if bits.OnesCount32((crypt>>20)&0x7f)%2 != 0 || boot&0x30 != 0 {
			return errors.New("encrypted or secure-boot devices are unsupported")
		}
		return nil
	}
	info, err := f.GetSecurityInfo()
	if err != nil {
		return fmt.Errorf("read security state: %w", err)
	}
	if info == nil {
		return errors.New("missing device security state")
	}
	if info.ParsedFlags.SecureBootEn || info.ParsedFlags.SecureDownloadEnable || bits.OnesCount8(info.FlashCryptCnt)%2 != 0 {
		return errors.New("encrypted, secure-boot, or secure-download devices are unsupported")
	}
	return nil
}
