// Package config loads inventories and prepares immutable firmware snapshots.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const MaxImageSize = 32 << 20

type Inventory struct {
	Version  int                `yaml:"version"`
	Profiles map[string]Profile `yaml:"profiles"`
	Devices  []Device           `yaml:"devices"`
	base     string
}
type Device struct {
	Name    string   `yaml:"name" json:"name"`
	Port    string   `yaml:"port" json:"port"`
	Profile string   `yaml:"profile" json:"profile"`
	Groups  []string `yaml:"groups" json:"groups"`
}
type Profile struct {
	Chip   string  `yaml:"chip" json:"chip"`
	Reset  string  `yaml:"reset" json:"reset"`
	Baud   int     `yaml:"baud" json:"baud"`
	Images []Image `yaml:"images" json:"images"`
}
type Image struct {
	File   string  `yaml:"file" json:"file"`
	Offset *uint32 `yaml:"offset" json:"offset"`
	SHA256 string  `yaml:"sha256,omitempty" json:"sha256,omitempty"`
}
type Binary struct {
	File   string `json:"file"`
	Offset uint32 `json:"offset"`
	SHA256 string `json:"sha256"`
	Data   []byte `json:"data"`
}
type Job struct {
	Device  Device   `json:"device"`
	Profile Profile  `json:"profile"`
	Images  []Binary `json:"images"`
}

func Load(path string) (*Inventory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := yaml.NewDecoder(io.LimitReader(f, 4<<20))
	d.KnownFields(true)
	var inv Inventory
	if err := d.Decode(&inv); err != nil {
		return nil, fmt.Errorf("decode inventory: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("inventory must contain exactly one YAML document")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	inv.base = filepath.Dir(abs)
	if inv.Version != 1 {
		return nil, fmt.Errorf("unsupported inventory version %d; expected 1", inv.Version)
	}
	if len(inv.Profiles) == 0 || len(inv.Devices) == 0 {
		return nil, errors.New("profiles and devices must not be empty")
	}
	for name, p := range inv.Profiles {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("empty profile name")
		}
		switch p.Chip {
		case "esp32", "esp32c3", "esp32s3":
		default:
			return nil, fmt.Errorf("profile %s: chip must be esp32, esp32c3, or esp32s3", name)
		}
		if p.Reset == "" {
			p.Reset = "default"
		}
		switch p.Reset {
		case "default", "usb-jtag", "no-reset":
		default:
			return nil, fmt.Errorf("profile %s: invalid reset mode", name)
		}
		if p.Baud == 0 {
			p.Baud = 460800
		}
		if p.Baud < 9600 || p.Baud > 3000000 {
			return nil, fmt.Errorf("profile %s: baud must be between 9600 and 3000000", name)
		}
		if len(p.Images) == 0 {
			return nil, fmt.Errorf("profile %s: no images", name)
		}
		for _, img := range p.Images {
			if strings.TrimSpace(img.File) == "" || img.Offset == nil {
				return nil, fmt.Errorf("profile %s: every image requires file and explicit offset", name)
			}
			if *img.Offset%4096 != 0 {
				return nil, fmt.Errorf("profile %s: offset must be aligned to a 4096-byte erase sector", name)
			}
			if img.SHA256 != "" {
				b, e := hex.DecodeString(img.SHA256)
				if e != nil || len(b) != 32 {
					return nil, fmt.Errorf("profile %s: invalid SHA-256", name)
				}
			}
		}
		inv.Profiles[name] = p
	}
	names := map[string]bool{}
	for _, dev := range inv.Devices {
		if strings.TrimSpace(dev.Name) == "" || strings.TrimSpace(dev.Port) == "" {
			return nil, errors.New("every device requires a name and port")
		}
		if names[dev.Name] {
			return nil, fmt.Errorf("duplicate device name %q", dev.Name)
		}
		names[dev.Name] = true
		if _, ok := inv.Profiles[dev.Profile]; !ok {
			return nil, fmt.Errorf("device %s: unknown profile %q", dev.Name, dev.Profile)
		}
		for _, g := range dev.Groups {
			if strings.TrimSpace(g) == "" {
				return nil, fmt.Errorf("device %s: empty group", dev.Name)
			}
		}
	}
	return &inv, nil
}

// PortKey also resolves POSIX symlinks to prevent concurrent opens of aliases.
func PortKey(port string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(strings.TrimPrefix(port, `\\.\`))
	}
	if p, err := filepath.EvalSymlinks(port); err == nil {
		port = p
	}
	if p, err := filepath.Abs(port); err == nil {
		port = p
	}
	return port
}

func (inv *Inventory) Select(names, groups []string, all bool) ([]Device, error) {
	if !all && len(names) == 0 && len(groups) == 0 {
		return nil, errors.New("select --device, --group, or --all explicitly")
	}
	if all && (len(names) > 0 || len(groups) > 0) {
		return nil, errors.New("--all cannot be combined with --device or --group")
	}
	wantNames, wantGroups := map[string]bool{}, map[string]bool{}
	for _, n := range names {
		wantNames[n] = false
	}
	for _, g := range groups {
		wantGroups[g] = false
	}
	out := []Device{}
	ports := map[string]string{}
	for _, dev := range inv.Devices {
		chosen := all
		if _, ok := wantNames[dev.Name]; ok {
			chosen = true
			wantNames[dev.Name] = true
		}
		for _, g := range dev.Groups {
			if _, ok := wantGroups[g]; ok {
				chosen = true
				wantGroups[g] = true
			}
		}
		if chosen {
			key := PortKey(dev.Port)
			if other, ok := ports[key]; ok {
				return nil, fmt.Errorf("devices %s and %s target the same serial port", other, dev.Name)
			}
			ports[key] = dev.Name
			out = append(out, dev)
		}
	}
	for n, found := range wantNames {
		if !found {
			return nil, fmt.Errorf("unknown device %q", n)
		}
	}
	for g, found := range wantGroups {
		if !found {
			return nil, fmt.Errorf("unknown group %q", g)
		}
	}
	return out, nil
}

// Prepare loads all selected firmware before any port is opened. Shared profiles
// reuse immutable byte slices; workers receive their own serialized copy.
func (inv *Inventory) Prepare(devices []Device) ([]Job, error) {
	cache := map[string][]Binary{}
	jobs := make([]Job, 0, len(devices))
	for _, dev := range devices {
		p := inv.Profiles[dev.Profile]
		images, ok := cache[dev.Profile]
		if !ok {
			for _, img := range p.Images {
				path := img.File
				if !filepath.IsAbs(path) {
					path = filepath.Join(inv.base, path)
				}
				data, err := readImage(path)
				if err != nil {
					return nil, fmt.Errorf("profile %s: %w", dev.Profile, err)
				}
				sum := sha256.Sum256(data)
				digest := hex.EncodeToString(sum[:])
				if img.SHA256 != "" && !strings.EqualFold(img.SHA256, digest) {
					return nil, fmt.Errorf("%s: SHA-256 mismatch", path)
				}
				if uint64(*img.Offset)+uint64(len(data)) > MaxImageSize {
					return nil, fmt.Errorf("%s: image exceeds 32 MiB address limit", path)
				}
				if err := ValidateFirmware(data, p.Chip); err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				images = append(images, Binary{File: path, Offset: *img.Offset, SHA256: digest, Data: data})
			}
			sort.Slice(images, func(i, j int) bool { return images[i].Offset < images[j].Offset })
			for i := 1; i < len(images); i++ {
				prev := images[i-1]
				end := uint64(prev.Offset) + (uint64(len(prev.Data))+4095)&^uint64(4095)
				if uint64(images[i].Offset) < end {
					return nil, fmt.Errorf("profile %s: overlapping flash erase regions", dev.Profile)
				}
			}
			cache[dev.Profile] = images
		}
		jobs = append(jobs, Job{Device: dev, Profile: p, Images: images})
	}
	return jobs, nil
}

func readImage(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > MaxImageSize {
		return nil, fmt.Errorf("%s: expected a nonempty regular binary of at most 32 MiB", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxImageSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > MaxImageSize {
		return nil, fmt.Errorf("%s: invalid image size", path)
	}
	return b, nil
}

// HeaderFlashSize preserves firmware header settings rather than silently
// substituting detected board capacity. Non-ESP data images return an empty size.
func HeaderFlashSize(data []byte, chip string) (string, error) {
	if len(data) == 0 || data[0] != 0xe9 {
		return "", nil
	}
	if len(data) < 24 {
		return "", errors.New("truncated ESP image header")
	}
	expected := map[string]uint16{"esp32": 0, "esp32c3": 5, "esp32s3": 9}[chip]
	actual := uint16(data[12]) | uint16(data[13])<<8
	if actual != expected {
		return "", fmt.Errorf("firmware chip ID %d does not match %s", actual, chip)
	}
	sizes := []string{"1MB", "2MB", "4MB", "8MB", "16MB", "32MB"}
	n := int(data[3] >> 4)
	if n >= len(sizes) {
		return "", errors.New("unsupported flash size in firmware header")
	}
	return sizes[n], nil
}
