# Compatibility and hardware validation

| Target | Software adapter | Physical validation |
| --- | --- | --- |
| ESP32, USB-to-UART | Implemented | Pending |
| ESP32-C3, USB-to-UART / native Serial-JTAG | Implemented | Pending |
| ESP32-S3, USB-to-UART / native Serial-JTAG | Implemented | Pending |
| ESP32-S3 CAM | Chip support only; board-specific firmware/layout required | Pending; first intended hardware target |

Windows, Linux, and macOS have CI tests and native builds. CI uses simulated
transports and has no attached ESP32 boards. Build status is not hardware evidence.

## ESP32-S3 CAM validation procedure

Record the exact manufacturer/model, module marking, flash/PSRAM capacity, host OS,
USB interface, driver, firmware framework/version, build commit, image hashes,
offsets, and partition layout. Obtain known-good firmware for that exact board.

1. Run `discover`; verify that discovery does not reset or interrupt the device.
2. Configure its port and reset method; run `devices` and an explicit dry run.
3. Flash one board and require verified bytes plus a reset request in the result.
4. Independently inspect serial boot output and camera/application behavior.
5. Repeat with multiple boards; unplug one or occupy its port and confirm the
   others complete. Confirm firmware preflight errors start no workers.
6. Test cancellation only with recoverable development hardware; record serial
   recovery steps. Do not test power interruption on irreplaceable devices.
7. Add dated results here only after observing physical behavior.

## Boundaries

- Serial ports are configured explicitly. Port names may change after unplugging
  or USB re-enumeration; check discovery before a batch. POSIX symlink aliases and
  Windows COM prefix/case aliases are deduplicated when possible.
- Board identity is not the same as chip identity: two S3 boards can need different
  flash layouts, pin mappings, or PSRAM settings. Select the proper profile.
- ESP images retain their declared flash size/mode/frequency; incompatible sizes
  fail rather than silently rewriting firmware settings. The flasher may pad the
  last word, and the flash erase granularity is 4096 bytes.
- No secure-boot provisioning, encryption, eFuse writes, filesystem migration,
  automatic rollback, OTA, or application health checks are implemented.
- Classic ESP32 uses read-only eFuse security checks because its ROM does not
  expose the newer security-info command. ESP32-C3/S3 use cached ROM security info.
- ESP32 image header IDs, first-image checksums/digests, and optional full-file
  SHA-256 are validated. A merged image's nested partitions cannot be inferred or
  exhaustively validated without its build's partition metadata.

Reference: [Espressif serial flashing documentation](https://docs.espressif.com/projects/esptool/en/latest/esp32/esptool/flashing-firmware.html).
