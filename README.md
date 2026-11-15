# puck-tak

A tiny TAK-compatible server for the ZTE MF688 WiFi puck (Android 4.4.4, 32-bit ARMv7, ~400 MB RAM).
One static Go binary, no dependencies, about 10-20 MB of RAM in use. It lets ATAK / iTAK / WinTAK
devices on the puck's WiFi share locations and pass data packages, with no internet needed.

Current version: **0.3.0**. It does plain TCP and TLS CoT, data packages, and the server-info
endpoints ATAK asks for, and installs from Windows, macOS or Linux. The video relay that was tried on Nov 10 is not included (see `docs/NOTES.md`).

## What it does

- **CoT relay** (TCP 8087, and TLS 8089 when `-cert`/`-key` are given): location, markers and other CoT
  events are forwarded between connected clients. The latest position or marker for each uid is
  kept, so a client that connects later sees everyone right away. Stale entries are dropped.
- **Data packages** (HTTP 8080): upload, search and download through the same `/Marti/sync/...`
  endpoints a TAK Server exposes. Files are stored under `/data/local/tmp/tak/data`.
  `http://192.168.100.1:8080/` is a small web page that lists packages and lets you upload from a browser.
- **Server info** (HTTP 8080): `/Marti/api/version`, `/Marti/api/version/config` and
  `/Marti/api/clientEndPoints` (connected callsigns), which ATAK/iTAK query on connect.
- **Auto-start on boot**, using the puck's `himiwebserver` service (see below).

There is **no authentication**: TLS encrypts the CoT stream but accepts any client. Anyone on the puck's WiFi can read and upload
packages, so the WiFi password is the only gate. The server listens on the WiFi bridge address only,
not on the cellular side.

## Install

**→ Step-by-step guide: [INSTALL.md](INSTALL.md)**

Short version, with the puck plugged in over USB and `adb devices` listing it:

| Windows | macOS / Linux |
|---------|---------------|
| double-click `deploy.cmd` | `bash deploy.sh` |

The installer copies `bin/puck-tak-armv7` to the puck, sets up auto-start, starts the server and
shows the log. On first start the puck creates its own TLS certificate, so OpenSSL isn't needed.
Then connect a phone to the puck's WiFi and open `http://192.168.100.1:8080/`.

## ATAK / WinTAK / iTAK

| Setting  | Plain                | TLS                                   |
|----------|----------------------|---------------------------------------|
| Address  | `192.168.100.1`      | `192.168.100.1`                       |
| Port     | `8087`               | `8089`                                |
| Protocol | `tcp`                | `ssl` (client must trust `cert.pem`)  |
| Auth     | off                  | off                                   |

Tested: ATAK on Android connects over plain TCP 8087 and relays positions, and they are replayed to
new clients. Data packages use the HTTP API on 8080. Every request is logged, and anything the server
doesn't recognise is logged as `UNHANDLED` with its path.

## Auto-start on boot

`init.d` doesn't run on this firmware and `/init.rc` lives on a read-only partition. What works is
borrowing the stock `himiwebserver` service, which init starts when `sys.himiweb_start=1`:

1. `scripts/himi-wrapper.sh` → `/data/local/tmp/himiwebserver-wrapper.sh` (starts puck-tak)
2. `/system/bin/himiwebserver` → symlink to that wrapper (original kept as `himiwebserver.real`)
3. `/data/local.prop` contains `sys.himiweb_start=1`

At boot init reads `local.prop`, starts "himiwebserver", and that starts puck-tak. The installer does
all three steps. Verified to survive a reboot. `deploy.cmd -Uninstall` / `bash deploy.sh --uninstall`
undoes them.

`scripts/legacy/` keeps the earlier init.d and boot-script attempts for reference; they don't run on this device.

## Everyday commands

    adb shell cat /data/local/tmp/tak/server.log                 # what happened
    adb shell 'kill $(cat /data/local/tmp/tak/pid)'              # stop
    adb shell /data/local/tmp/himiwebserver-wrapper.sh           # start (same as at boot)

The log rotates at 1 MB (one old file is kept). Add `-v` to log every CoT event while debugging.

Options: `-cot-port`, `-http-port`, `-max-upload-mb` (default 100), `-max-store-mb` (default 500),
`-bind`, `-data`, `-log`, `-pidfile`, `-daemon`, `-v`, and for TLS `-cert`, `-key`, `-cot-tls-port` (default 8089)
and `-gen-cert` (create a self-signed pair at the `-cert`/`-key` paths if neither file exists yet).
Without `-cert`/`-key` the TLS port is simply not opened.

## Files

| Path | What |
|------|------|
| `deploy.cmd`, `deploy.ps1` | Installer for Windows |
| `deploy.sh` | Installer for macOS / Linux |
| `bin/puck-tak-armv7` | Server binary for the puck |
| `*.go`, `go.mod` | Server source |
| `test_e2e.py` | End-to-end tests |
| `scripts/himi-wrapper.sh` | Boot wrapper the installer puts on the puck |
| `scripts/legacy/` | Old auto-start attempts that don't work on this firmware (kept for reference) |
| `docs/NOTES.md` | Hardware details and what was tried along the way |

## Build from source

    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "-s -w" -o bin/puck-tak-armv7 .
    go build -o puck-tak . && python3 test_e2e.py ./puck-tak /tmp/pucktest     # 47 checks

## Still to do

- Turn off the unauthenticated network adb listener (port 22345) on the puck.
- A documented way to make ATAK trust the TLS certificate (truststore), so 8089 can be used.
- Client-certificate auth for TLS.

See `docs/NOTES.md` for hardware details and what was tried along the way.
