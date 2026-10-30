# Copilot instructions

Read `AGENTS.md` before changing code that touches platform-specific UI, goroutine
lifecycles, Windows message pumps, tray/hotkey handling, or build/release behavior.
It is the repository's detailed source of hard constraints. `docs/MODULE-CONTRACT.md`
defines the module API; `docs/DEVELOPMENT.md` covers local setup.

## Build, test, and checks

Requires Go 1.27.1 or later. Run the preflight before building; it checks the Go
version, build-tag contract, dependencies, formatting, workflows, and `go vet`.

```bash
bash scripts/preflight.sh windows amd64
bash scripts/preflight.sh windows amd64 --with-tests
go build ./...                                  # compile for the current host
go test -count=1 ./internal/... ./modules/...
go test ./internal/core -run '^TestParseHotkeyCanonicalizes$' -count=1
go test -race -count=1 ./internal/... ./modules/... # requires cgo
go vet ./...
gofmt -l .                                      # format with gofmt -w <files>
bash scripts/check-emoji.sh
bash scripts/check-workflows.sh
bash scripts/gen-logo.sh --check
bash scripts/test-linux-tray-dbus.sh              # Docker-backed D-Bus integration test
```

Use the release build script for target artifacts; it fixes `CGO_ENABLED=0`, the
Wails `production` tag, and Windows GUI-subsystem settings, then validates the
artifact:

```bash
bash scripts/build.sh windows amd64 dist/pcmannager-windows-amd64.exe
bash scripts/build.sh linux amd64 dist/pcmannager-linux-amd64
```

The supported release targets are `windows/amd64`, `linux/amd64`,
`darwin/amd64`, and `darwin/arm64`. Do not change build tags outside
`scripts/build-env.sh`. After changing dependencies, run `go mod tidy`.

## Architecture

- `main.go` composes the application, registers feature modules, and starts the
  tray/panel and platform run loop. `internal/app` owns module registration and
  lifecycle orchestration, configuration, logging, events, hotkeys, tray, and
  panel integration.
- `internal/core` defines the `Module` lifecycle and shared `Context`, `Option`,
  `Action`, `State`, event-bus, and hotkey contracts. `internal/config` persists
  application and per-module settings. Each `modules/<id>` package implements
  one feature; `modules/preferences` presents the registered modules' settings.
- `internal/app` implements `server.Provider`. `internal/server` exposes the
  panel REST/SSE API, and `internal/wailsapp` provides the Windows native-window
  transport. Both render the single embedded frontend at
  `internal/panel/index.html` and use the same provider contract. The HTTP panel
  listens only on loopback; mutation routes are protected by request-token,
  content-type, fetch-site, and host checks.
- Platform services are isolated in paired build-tagged files: `internal/winui`
  wraps Win32 without cgo, while `internal/tray` and `internal/sysutil` provide
  platform-specific tray and system integration. Non-Windows UI implementations
  often degrade to no-ops; a successful cross-build does not imply feature parity.

## Repository-specific conventions

- Module IDs are stable API/configuration keys and must match their
  `modules/<id>` directory. Implement `core.Module`, usually by embedding
  `core.Base`; use `func NewFeature() core.Module`, register in `main.go`, and
  keep each option's declared default aligned with `internal/config` defaults.
  Declare restart-required module options with `core.Option.Restart`.
- Resolve configuration and data paths through `internal/paths`; the
  `PCMANNAGER_CONFIG` override must move both config and data together, while
  `app.data_dir` takes precedence.
- Platform-specific source must preserve matching APIs across supported build
  variants. Files importing `internal/winui` must be Windows-only. Avoid cgo;
  maintain `CGO_ENABLED=0` builds. Windows UI/message-pump work has thread
  affinity, and `Stop`/shutdown paths must remain bounded and safe to repeat;
  consult `AGENTS.md` for the relevant locking and thread rules.
- Global hotkey availability probing only works on Windows. The panel still
  permits syntax-valid hotkey settings on other platforms and labels them as
  unverified there.
- Modules report logs, progress, notices, and state through `core.Context.Bus`;
  use the structured logger (`Logger.Info("message", "key", value)`). Background
  work must observe the module context and stop cleanly.
- Client UI strings and assets must not contain emoji. Use the existing inline
  SVG/icon resources. `internal/logo` is the source for brand geometry; regenerate
  site SVGs with `bash scripts/gen-logo.sh` rather than editing generated assets.
- `reference/` contains third-party git submodules: do not edit or import them
  from application code. Keep repository changes in the first-party packages.
