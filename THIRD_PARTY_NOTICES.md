# Third-party notices

espfleet's original source is MIT-licensed. Its dependencies retain their own
licenses and copyrights. Distribute this file and `third_party/licenses` alongside
compiled binaries (the CI artifact packaging does this).

| Component | Pinned version | License file |
| --- | --- | --- |
| TinyGo espflasher | v0.9.2 | `tinygo.org_x_espflasher-LICENSE` (BSD-3-Clause) |
| Cobra | v1.10.2 | `github.com_spf13_cobra-LICENSE.txt` (Apache-2.0) |
| pflag | v1.0.9 | `github.com_spf13_pflag-LICENSE` (BSD-3-Clause) |
| mousetrap | v1.1.0 | `github.com_inconshreveable_mousetrap-LICENSE` (Apache-2.0) |
| go.bug.st/serial | v1.8.0 | `go.bug.st_serial-LICENSE` (BSD-3-Clause) |
| golang.org/x/sys | v0.43.0 | `golang.org_x_sys-LICENSE` (BSD-3-Clause) |
| yaml.v3 | v3.0.1 | `gopkg.in_yaml.v3-LICENSE` and `gopkg.in_yaml.v3-NOTICE` |
| Espressif esp-flasher-stub (embedded by espflasher) | v1.1.0 | `esp-flasher-stub-LICENSE-APACHE` (Apache-2.0 option) |

Files above are under `third_party/licenses`. The embedded flasher stubs are
Copyright 2025 Espressif Systems (Shanghai) CO LTD, distributed under their
Apache-2.0 license option. The upstream stub project also offers an MIT option.
No third-party source or stub binaries have been modified in this repository.

The Go standard library/runtime are distributed under the Go BSD-style license,
also reproduced in `third_party/licenses/Go-LICENSE`.

Sources: [espflasher](https://github.com/tinygo-org/espflasher),
[esp-flasher-stub](https://github.com/espressif/esp-flasher-stub/tree/v1.1.0),
[Cobra](https://github.com/spf13/cobra), [pflag](https://github.com/spf13/pflag),
[mousetrap](https://github.com/inconshreveable/mousetrap),
[serial](https://github.com/bugst/go-serial), [x/sys](https://go.googlesource.com/sys),
[yaml](https://github.com/go-yaml/yaml/tree/v3.0.1), [Go](https://go.dev/LICENSE).
