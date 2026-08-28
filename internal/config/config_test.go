package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const valid = `version: 1
profiles:
  sensor:
    chip: esp32s3
    images:
      - file: app.bin
        offset: 0x10000
devices:
  - name: door
    port: COM8
    profile: sensor
    groups: [sensors]
  - name: window
    port: COM9
    profile: sensor
    groups: [sensors]
`

func fixture(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet.yaml")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.bin"), []byte("test data partition"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestLoadAndPrepareRelativeFiles(t *testing.T) {
	path := fixture(t, valid)
	inv, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := inv.Select([]string{"door"}, []string{"sensors"}, false)
	if err != nil || len(d) != 2 {
		t.Fatalf("selection: %v %v", d, err)
	}
	jobs, err := inv.Prepare(d)
	if err != nil {
		t.Fatal(err)
	}
	if jobs[0].Profile.Baud != 460800 || jobs[0].Profile.Reset != "default" {
		t.Fatal("missing defaults")
	}
	img := jobs[0].Images[0]
	if !filepath.IsAbs(img.File) || len(img.SHA256) != 64 || string(img.Data) != "test data partition" {
		t.Fatalf("bad image: %+v", img)
	}
}
func TestInvalidConfiguration(t *testing.T) {
	cases := map[string]string{
		"malformed":          "version: [",
		"unknown field":      valid + "surprise: true\n",
		"version":            strings.Replace(valid, "version: 1", "version: 2", 1),
		"missing offset":     strings.Replace(valid, "        offset: 0x10000\n", "", 1),
		"negative offset":    strings.Replace(valid, "0x10000", "-1", 1),
		"unaligned":          strings.Replace(valid, "0x10000", "0x10001", 1),
		"duplicate name":     strings.Replace(valid, "name: window", "name: door", 1),
		"unknown profile":    strings.Replace(valid, "profile: sensor", "profile: missing", 1),
		"bad chip":           strings.Replace(valid, "esp32s3", "esp8266", 1),
		"bad hash":           strings.Replace(valid, "offset: 0x10000", "offset: 0x10000\n        sha256: nope", 1),
		"multiple documents": valid + "---\nversion: 1\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(fixture(t, src)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
func TestSelectionValidation(t *testing.T) {
	inv, err := Load(fixture(t, valid))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		names, groups []string
		all           bool
	}{{nil, nil, false}, {[]string{"missing"}, nil, false}, {nil, []string{"missing"}, false}, {[]string{"door"}, nil, true}} {
		if _, err := inv.Select(tc.names, tc.groups, tc.all); err == nil {
			t.Fatal("expected selection error")
		}
	}
	inv.Devices[1].Port = inv.Devices[0].Port
	if _, err := inv.Select(nil, nil, true); err == nil {
		t.Fatal("duplicate target allowed")
	}
}
func TestPreflightFailures(t *testing.T) {
	cases := map[string]string{
		"missing":  strings.Replace(valid, "app.bin", "missing.bin", 1),
		"hash":     strings.Replace(valid, "offset: 0x10000", "offset: 0x10000\n        sha256: "+strings.Repeat("0", 64), 1),
		"overlap":  strings.Replace(valid, "offset: 0x10000", "offset: 0x10000\n      - file: app.bin\n        offset: 0x10000", 1),
		"overflow": strings.Replace(valid, "0x10000", "0xfffff000", 1),
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			inv, err := Load(fixture(t, src))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inv.Prepare(inv.Devices); err == nil {
				t.Fatal("expected preflight error")
			}
		})
	}
}
func TestEraseRegionOverlap(t *testing.T) {
	src := strings.Replace(valid, "offset: 0x10000", "offset: 0x10000\n      - file: app.bin\n        offset: 0x11000", 1)
	path := fixture(t, src)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "app.bin"), make([]byte, 4097), 0600); err != nil {
		t.Fatal(err)
	}
	inv, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inv.Prepare(inv.Devices); err == nil {
		t.Fatal("overlapping erase sectors allowed")
	}
}
func TestImageHeader(t *testing.T) {
	data := make([]byte, 24)
	data[0] = 0xe9
	data[3] = 0x40
	data[12] = 9
	if size, err := HeaderFlashSize(data, "esp32s3"); err != nil || size != "16MB" {
		t.Fatalf("%s %v", size, err)
	}
	if _, err := HeaderFlashSize(data, "esp32c3"); err == nil {
		t.Fatal("wrong chip accepted")
	}
	if _, err := HeaderFlashSize(data[:4], "esp32s3"); err == nil {
		t.Fatal("short header accepted")
	}
}
