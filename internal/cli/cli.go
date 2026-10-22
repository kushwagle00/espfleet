package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kushwagle00/espfleet/internal/batch"
	"github.com/kushwagle00/espfleet/internal/config"
	"github.com/kushwagle00/espfleet/internal/discovery"
	"github.com/kushwagle00/espfleet/internal/transport"
	"github.com/spf13/cobra"
)

var Version = "dev"

type Dependencies struct {
	Discover func() ([]discovery.Port, error)
	Runner   batch.Runner
}
type exitError struct {
	code     int
	err      error
	reported bool
}

func (e *exitError) Error() string { return e.err.Error() }
func runtimeError(err error) error { return &exitError{code: 1, err: err} }

func Execute(ctx context.Context, args []string, out, errOut io.Writer, deps Dependencies) int {
	if deps.Discover == nil {
		deps.Discover = discovery.List
	}
	if deps.Runner == nil {
		deps.Runner = transport.Process{}
	}
	var asJSON bool
	root := &cobra.Command{Use: "espfleet", Short: "Batch serial firmware updates for ESP32 gadgets", SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(args)
	root.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "Write one JSON result to stdout; progress goes to stderr")
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(discoverCommand(deps, &asJSON), devicesCommand(deps, &asJSON), flashCommand(deps, &asJSON), versionCommand(&asJSON))
	// Preserve JSON error output even if flag parsing stops at an earlier bad flag.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		switch arg {
		case "--json", "--json=true":
			asJSON = true
		case "--json=false":
			asJSON = false
		}
	}
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	code := 2
	var exit *exitError
	if errors.As(err, &exit) {
		code = exit.code
		if exit.reported {
			return code
		}
	}
	if asJSON {
		_ = json.NewEncoder(out).Encode(map[string]any{"error": err.Error(), "exit_code": code})
	} else {
		fmt.Fprintln(errOut, "Error:", err)
	}
	return code
}

func writeJSON(c *cobra.Command, v any) error {
	if err := json.NewEncoder(c.OutOrStdout()).Encode(v); err != nil {
		return runtimeError(err)
	}
	return nil
}
func discoverCommand(deps Dependencies, asJSON *bool) *cobra.Command {
	return &cobra.Command{Use: "discover", Short: "List serial candidates without opening or resetting devices", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		ports, err := deps.Discover()
		if err != nil {
			return runtimeError(err)
		}
		if *asJSON {
			return writeJSON(c, map[string]any{"kind": "serial_candidates", "ports": ports})
		}
		fmt.Fprintln(c.OutOrStdout(), "Serial candidates (USB metadata does not confirm an ESP32):")
		w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "PORT\tUSB\tVID:PID\tSERIAL\tPRODUCT")
		for _, p := range ports {
			fmt.Fprintf(w, "%s\t%t\t%s:%s\t%s\t%s\n", p.Name, p.USB, p.VID, p.PID, p.SerialNumber, p.Product)
		}
		return w.Flush()
	}}
}

type listedDevice struct {
	config.Device
	Present bool `json:"present"`
}

func devicesCommand(deps Dependencies, asJSON *bool) *cobra.Command {
	var path string
	c := &cobra.Command{Use: "devices", Short: "List configured devices and serial-port presence", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		inv, err := config.Load(path)
		if err != nil {
			return err
		}
		ports, err := deps.Discover()
		if err != nil {
			return runtimeError(err)
		}
		present := map[string]bool{}
		for _, p := range ports {
			present[config.PortKey(p.Name)] = true
		}
		rows := make([]listedDevice, 0, len(inv.Devices))
		for _, d := range inv.Devices {
			rows = append(rows, listedDevice{d, present[config.PortKey(d.Port)]})
		}
		if *asJSON {
			return writeJSON(c, map[string]any{"devices": rows})
		}
		w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPORT\tPROFILE\tGROUPS\tPORT PRESENT")
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%t\n", r.Name, r.Port, r.Profile, strings.Join(r.Groups, ","), r.Present)
		}
		return w.Flush()
	}}
	c.Flags().StringVar(&path, "config", "fleet.yaml", "Inventory YAML file")
	return c
}

type plannedImage struct {
	File   string `json:"file"`
	Offset uint32 `json:"offset"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type plannedDevice struct {
	config.Device
	Chip   string         `json:"chip"`
	Reset  string         `json:"reset"`
	Baud   int            `json:"baud"`
	Images []plannedImage `json:"images"`
}
type summary struct {
	Results   []batch.Result `json:"results"`
	Succeeded int            `json:"succeeded"`
	Failed    int            `json:"failed"`
	Cancelled int            `json:"cancelled"`
	Skipped   int            `json:"skipped"`
}

func flashCommand(deps Dependencies, asJSON *bool) *cobra.Command {
	var path string
	var names, groups []string
	var all, dry bool
	var parallel int
	var timeout time.Duration
	c := &cobra.Command{Use: "flash", Short: "Flash explicitly selected devices and verify every image", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if parallel < 1 || parallel > 32 {
			return errors.New("--jobs must be between 1 and 32")
		}
		if timeout <= 0 {
			return errors.New("--timeout must be positive")
		}
		inv, err := config.Load(path)
		if err != nil {
			return err
		}
		selected, err := inv.Select(names, groups, all)
		if err != nil {
			return err
		}
		jobs, err := inv.Prepare(selected)
		if err != nil {
			return err
		}
		if dry {
			planned := make([]plannedDevice, 0, len(jobs))
			for _, j := range jobs {
				p := plannedDevice{Device: j.Device, Chip: j.Profile.Chip, Reset: j.Profile.Reset, Baud: j.Profile.Baud, Images: []plannedImage{}}
				for _, img := range j.Images {
					p.Images = append(p.Images, plannedImage{img.File, img.Offset, len(img.Data), img.SHA256})
				}
				planned = append(planned, p)
			}
			if *asJSON {
				return writeJSON(c, map[string]any{"dry_run": true, "hardware_checked": false, "devices": planned})
			}
			fmt.Fprintln(c.OutOrStdout(), "Dry run: validated files and configuration; no ports opened, hardware not checked.")
			for _, p := range planned {
				fmt.Fprintf(c.OutOrStdout(), "%s (%s, %s, reset=%s, baud=%d)\n", p.Name, p.Port, p.Chip, p.Reset, p.Baud)
				for _, img := range p.Images {
					fmt.Fprintf(c.OutOrStdout(), "  0x%08x  %d bytes  %s  sha256=%s\n", img.Offset, img.Bytes, img.File, img.SHA256)
				}
			}
			return nil
		}
		last := map[string]int{}
		emit := func(e batch.Event) {
			if e.Stage == "writing" && e.Total > 0 {
				pct := 100 * e.Current / e.Total
				bucket := pct / 10
				if old, ok := last[e.Device]; ok && old == bucket {
					return
				}
				last[e.Device] = bucket
				fmt.Fprintf(c.ErrOrStderr(), "[%s] writing %d%%\n", e.Device, pct)
			} else {
				if e.Stage == "writing" {
					delete(last, e.Device)
				}
				fmt.Fprintf(c.ErrOrStderr(), "[%s] %s %s\n", e.Device, e.Stage, e.Message)
			}
		}
		results := batch.Run(c.Context(), jobs, parallel, timeout, deps.Runner, emit)
		s := summary{Results: results}
		for _, r := range results {
			switch r.Status {
			case "success":
				s.Succeeded++
			case "cancelled":
				s.Cancelled++
			case "skipped":
				s.Skipped++
			default:
				s.Failed++
			}
		}
		if *asJSON {
			if err := writeJSON(c, s); err != nil {
				return err
			}
		} else {
			w := tabwriter.NewWriter(c.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "DEVICE\tPORT\tRESULT\tVERIFIED\tDETAIL")
			for _, r := range results {
				fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\n", r.Device, r.Port, r.Status, r.Verified, r.Error)
			}
			if err := w.Flush(); err != nil {
				return runtimeError(err)
			}
			fmt.Fprintf(c.OutOrStdout(), "%d succeeded, %d failed, %d cancelled, %d skipped. Application health was not checked.\n", s.Succeeded, s.Failed, s.Cancelled, s.Skipped)
		}
		if s.Failed+s.Cancelled+s.Skipped > 0 {
			return &exitError{code: 1, err: errors.New("one or more devices did not complete"), reported: true}
		}
		return nil
	}}
	c.Flags().StringVar(&path, "config", "fleet.yaml", "Inventory YAML file")
	c.Flags().StringArrayVar(&names, "device", nil, "Device name (repeatable)")
	c.Flags().StringArrayVar(&groups, "group", nil, "Group name (repeatable; union with --device)")
	c.Flags().BoolVar(&all, "all", false, "Select every configured device")
	c.Flags().BoolVar(&dry, "dry-run", false, "Validate and show the plan without opening ports")
	c.Flags().IntVar(&parallel, "jobs", 2, "Maximum concurrent devices (1-32)")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Maximum time per device, including connection and verification")
	return c
}

func versionCommand(asJSON *bool) *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print build information", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		v := map[string]string{"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}
		if b, ok := debug.ReadBuildInfo(); ok {
			if Version == "dev" && b.Main.Version != "" && b.Main.Version != "(devel)" {
				v["version"] = b.Main.Version
			}
			for _, s := range b.Settings {
				if s.Key == "vcs.revision" || s.Key == "vcs.modified" {
					v[s.Key] = s.Value
				}
			}
		}
		if *asJSON {
			return writeJSON(c, v)
		}
		fmt.Fprintf(c.OutOrStdout(), "espfleet %s (%s %s/%s) commit=%s modified=%s\n", v["version"], v["go"], v["os"], v["arch"], v["vcs.revision"], v["vcs.modified"])
		return nil
	}}
}
