# espfleet

[![CI](https://github.com/kushwagle00/espfleet/actions/workflows/ci.yml/badge.svg)](https://github.com/kushwagle00/espfleet/actions/workflows/ci.yml)

A standalone Go CLI for updating multiple ESP32 sensors and gadgets over serial.
Give devices names, organize them into groups, assign firmware profiles, and flash
them in bounded parallel batches with individual results.

**Initial development release. Physical board validation is pending.** ESP32,
ESP32-C3, and ESP32-S3 are software targets. The first planned hardware target is
an ESP32-S3 CAM; its exact model and flash layout must be supplied before testing.

## Install

Build with Go 1.25 or newer. Python and esptool are not required.

```sh
go install github.com/kushwagle00/espfleet@latest
```

Or build a checkout:

```sh
go build -o dist/espfleet .
```

On Windows use `go build -o dist/espfleet.exe .`. The OS may need a driver for your
USB-to-serial adapter. macOS builds use the system C toolchain for serial device
enumeration. Normal use requires only the built executable and any board driver.
CI artifacts include executables and third-party license notices.

## Quick start

1. Compile firmware with your existing Arduino, ESP-IDF, PlatformIO, TinyGo, or
   other build system. Keep its binary files and exact flash offsets.
2. Run `espfleet discover` to find candidate serial ports. This does not open ports
   or reset devices; USB IDs alone cannot establish board identity.
3. Copy `examples/fleet.yaml` to `fleet.yaml`. Replace the example ports, binaries,
   and offsets with your build's values. Paths are relative to the YAML file.
4. Inspect the inventory and dry run before the first flash:

```sh
espfleet devices --config fleet.yaml
espfleet flash --config fleet.yaml --group sensors --dry-run
espfleet flash --config fleet.yaml --group sensors
```

Select one or several devices, or all configured devices explicitly:

```sh
espfleet flash --device door-01 --device door-02 --jobs 2
espfleet flash --group sensors --group cameras --timeout 5m
espfleet flash --all --json > results.json
espfleet version --json
```

`--device` and `--group` select a union without duplicates. `--all` cannot be
combined with either. Discovery never automatically selects devices to flash.

## Firmware profiles

```yaml
version: 1
profiles:
  door-s3:
    chip: esp32s3
    reset: default
    baud: 460800
    images:
      # Illustrative app-only offset: use YOUR build's offset and ensure the
      # correct bootloader/partition table are already installed.
      - file: firmware/door-app.bin
        offset: 0x10000
        # sha256: <optional 64-character digest of the entire file>
devices:
  - name: door-01
    port: COM8
    profile: door-s3
    groups: [sensors, downstairs]
  - name: door-02
    port: COM9
    profile: door-s3
    groups: [sensors, upstairs]
```

Multiple images can specify bootloader, partition table, application, or raw data
partitions. A merged binary uses its build-provided base offset. espfleet never
guesses offsets, partition layouts, camera pin mappings, or PSRAM settings.

- Chips: `esp32`, `esp32c3`, `esp32s3`.
- Reset modes: `default` (USB-to-UART), `usb-jtag` (native USB Serial/JTAG),
  `no-reset` (already in download mode). Default: `default`.
- Baud: 9600–3000000, default 460800. The backend may cap rates for specific chips.
- Images: nonempty binary files up to 32 MiB, explicit 4096-byte-aligned offsets,
  optional SHA-256. The selected images' erase regions must not overlap.
- Unknown YAML fields, duplicate device names, and unknown profile references fail
  validation. Selected serial targets must be unique, including resolved symlinks.
- `--jobs` defaults to 2 (range 1–32); `--timeout` defaults to 5 minutes per board.

## Failure behavior

**Board errors continue; confirmed firmware errors stop the batch.**

| Failure | Behavior |
| --- | --- |
| Invalid config, missing file, SHA-256 mismatch, malformed ESP image, overlapping offsets | Reject the entire selected batch before opening any ports; exit 2 |
| Busy/missing port, disconnect, wrong physical chip, insufficient flash, security restrictions | Fail that board and continue with other boards |
| Flash verification mismatch or verification command error | Fail that board and continue; this can indicate a cable/device problem and is not proof the source firmware is defective |
| Confirmed firmware error detected by a worker | Stop scheduling new boards; mark queued boards `skipped`; allow in-flight operations to finish |
| Ctrl+C or per-device timeout | Terminate affected workers, release serial handles, and report cancellation; no automatic write retry |

All selected firmware is loaded and checked before any worker starts, so source
errors normally stop the batch during preflight. Workers use those byte snapshots
even if files on disk change afterward. ESP image checks cover the first image's
segment structure, chip ID, checksum, and appended digest when present. Raw data
partitions have no universal format; use SHA-256 to check their integrity.

Flashing cannot prove firmware is logically correct. A valid image may still
crash or use the wrong board pin mapping. Post-boot health checks are a future
feature; do not interpret verified writes as confirmation that a sensor works.

## Results and guarantees

Human-readable progress is written to stderr. `--json` writes a single JSON object
to stdout, including dry runs and command errors. Batch results contain device,
port, status (`success`, `failed`, `cancelled`, `skipped`), error details,
`verified`, `reset_requested`, `application_health`, and duration.

Exit codes: **0** complete success, **1** device/runtime failure or incomplete batch,
**2** invalid command, configuration, or firmware preflight.

Before writing, each worker detects the physical chip and compares the profile,
checks security state and flash capacity, then writes the requested regions. A
strict digest pass verifies every image against the prepared bytes. Only verified
boards receive a reset request. A reset request does not confirm a successful boot.

No whole-chip erase or eFuse provisioning is performed. Flashing still erases the
sectors covering each selected image, including trailing bytes in its last sector.
Cancellation or power loss can leave incomplete firmware; recover over serial.
Secure-boot and encrypted devices are outside the initial scope and are rejected
when security checks detect them. The tool does not alter security settings.

## Architecture and roadmap

The CLI, configuration, discovery, batch coordinator, and transport live in
separate internal packages. Each device uses a child instance of the same
executable. This isolates upstream chip state and permits cancellation even when
a driver blocks. No external helper executable is needed.

Serial flashing uses [TinyGo espflasher](https://github.com/tinygo-org/espflasher).
The project focuses on inventory and multi-device orchestration.

Next milestones: hardware validation, stable device identity, OTA protocol
adapters, mDNS discovery, and delivery to intermittently awake sensors. Wireless
updates require compatible OTA support already installed in device firmware;
Wi-Fi connectivity alone cannot provide it. No OTA commands are implemented yet.

See [compatibility](docs/compatibility.md), [contributing](CONTRIBUTING.md), and
[third-party notices](THIRD_PARTY_NOTICES.md).
