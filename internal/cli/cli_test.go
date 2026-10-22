package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/kushwagle00/espfleet/internal/batch"
	"github.com/kushwagle00/espfleet/internal/config"
	"github.com/kushwagle00/espfleet/internal/discovery"
)

type fakeRunner struct {
	calls atomic.Int32
	fail  bool
}

func (f *fakeRunner) Run(_ context.Context, j config.Job, e func(batch.Event)) (batch.Outcome, error) {
	f.calls.Add(1)
	e(batch.Event{Device: j.Device.Name, Stage: "writing", Current: 1, Total: 1})
	if f.fail && j.Device.Name == "one" {
		return batch.Outcome{}, errors.New("port unavailable")
	}
	return batch.Outcome{Verified: true, ResetRequested: true}, nil
}
func inventory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet.yaml")
	src := `version: 1
profiles:
  test:
    chip: esp32s3
    images: [{file: data.bin, offset: 0x10000}]
devices:
  - {name: one, port: COM8, profile: test, groups: [sensors]}
  - {name: two, port: COM9, profile: test, groups: [sensors]}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), []byte("raw data"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func decodeOne(t *testing.T, out *bytes.Buffer) map[string]any {
	t.Helper()
	d := json.NewDecoder(out)
	var v map[string]any
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout contains extra output: %v", err)
	}
	return v
}
func TestDryRunNeverTouchesHardware(t *testing.T) {
	path := inventory(t)
	r := &fakeRunner{}
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"flash", "--config", path, "--all", "--dry-run", "--json"}, &out, &stderr, Dependencies{Runner: r, Discover: func() ([]discovery.Port, error) { t.Fatal("discovery called"); return nil, nil }})
	if code != 0 || r.calls.Load() != 0 {
		t.Fatalf("code=%d calls=%d stderr=%s", code, r.calls.Load(), stderr.String())
	}
	v := decodeOne(t, &out)
	if v["hardware_checked"] != false {
		t.Fatal(v)
	}
}
func TestMixedJSONSummaryAndExitCode(t *testing.T) {
	path := inventory(t)
	r := &fakeRunner{fail: true}
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"flash", "--config", path, "--all", "--json"}, &out, &stderr, Dependencies{Runner: r})
	if code != 1 || r.calls.Load() != 2 {
		t.Fatalf("code=%d calls=%d", code, r.calls.Load())
	}
	v := decodeOne(t, &out)
	if v["succeeded"] != float64(1) || v["failed"] != float64(1) {
		t.Fatal(v)
	}
	if stderr.Len() == 0 {
		t.Fatal("missing progress")
	}
}
func TestInvalidInputsNeverRun(t *testing.T) {
	path := inventory(t)
	for _, args := range [][]string{{"flash", "--config", path}, {"flash", "--config", path, "--all", "--jobs", "0"}, {"flash", "--config", path, "--device", "missing"}, {"flash", "--config", path, "--all", "--device", "one"}, {"flash", "--bogus"}} {
		r := &fakeRunner{}
		var out, stderr bytes.Buffer
		if code := Execute(context.Background(), args, &out, &stderr, Dependencies{Runner: r}); code != 2 || r.calls.Load() != 0 {
			t.Fatalf("%v code=%d", args, code)
		}
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), "data.bin")); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	var out, stderr bytes.Buffer
	if code := Execute(context.Background(), []string{"flash", "--config", path, "--all", "--json"}, &out, &stderr, Dependencies{Runner: r}); code != 2 || r.calls.Load() != 0 {
		t.Fatalf("firmware preflight code=%d", code)
	}
	decodeOne(t, &out)
}
func TestReadOnlyCommands(t *testing.T) {
	path := inventory(t)
	deps := Dependencies{Discover: func() ([]discovery.Port, error) {
		return []discovery.Port{{Name: "COM8", USB: true, VID: "303a", PID: "1001"}}, nil
	}}
	for _, args := range [][]string{{"discover", "--json"}, {"devices", "--config", path, "--json"}, {"version", "--json"}} {
		var out, stderr bytes.Buffer
		if code := Execute(context.Background(), args, &out, &stderr, deps); code != 0 {
			t.Fatalf("%v %s", args, stderr.String())
		}
		v := decodeOne(t, &out)
		if args[0] == "devices" {
			rows := v["devices"].([]any)
			if rows[0].(map[string]any)["present"] != true || rows[1].(map[string]any)["present"] != false {
				t.Fatal(v)
			}
		}
	}
}

func TestJSONFlagError(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Execute(context.Background(), []string{"flash", "--bogus", "--json"}, &out, &stderr, Dependencies{})
	if code != 2 {
		t.Fatalf("exit %d", code)
	}
	if v := decodeOne(t, &out); v["exit_code"] != float64(2) {
		t.Fatal(v)
	}
}
