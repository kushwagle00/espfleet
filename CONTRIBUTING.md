# Contributing

Use Go 1.25 or newer. Run from the repository root:

```sh
gofmt -w main.go internal
go mod tidy
go test ./...
go vet ./...
go build ./...
```

Also run `go test -race ./...` where a supported C compiler is installed. Linux CI
runs race detection. macOS serial enumeration uses cgo and requires Xcode Command
Line Tools; Windows race detection needs a compatible MinGW compiler.

Tests must not open real serial ports. Inject a batch runner or simulated flasher.
Preserve the distinction between device failures (continue) and confirmed firmware
failures (stop scheduling). Add regression tests for both sequential and concurrent
execution when changing failure policy. Verify one JSON object on stdout, with
progress only on stderr.

Internal packages are not a stable public Go API. Configuration uses `version: 1`;
reject unknown fields and preserve explicit selection and offsets. Never make
discovery flash devices automatically. Keep firmware snapshots immutable and run
all selected profiles through preflight before opening ports.

The serial adapter uses one subprocess per device to isolate upstream mutable
chip definitions and allow reliable cancellation. Do not replace this with shared
in-process flashers without auditing the upstream concurrency behavior.

For dependencies, pin versions in go.mod/go.sum and refresh relevant files in
`third_party/licenses`. Keep embedded flasher-stub notices in distributions.
Hardware support claims require recorded physical tests; follow
`docs/compatibility.md`. Keep camera firmware, binaries, private device inventories,
and credentials out of the repository.
