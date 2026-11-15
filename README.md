# puck-tak

A tiny TAK-compatible server for the ZTE MF688 WiFi puck (Android 4.4.4, 32-bit ARMv7, ~400 MB RAM).
One static Go binary, no dependencies, about 10-20 MB of RAM in use. It lets ATAK / iTAK / WinTAK
devices on the puck's WiFi share locations and pass data packages, with no internet needed.

> ⚠️ **Source vs. binary:** the Go source in this repo is **v0.1.0 (Oct 18)**: CoT relay on TCP 8087
> and data packages over HTTP 8080. The binary in `bin/puck-tak-armv7` (Nov 10) is a later build
> that also has **TLS on 8089** (`-cert`/`-key`) and the experimental video endpoints
> (`-video-multicast`, `/video`, `/stream`). The source for those additions isn't in this repo yet.
> Building from this source and then running `deploy.sh` as-is will fail, because the old build
> doesn't know `-cert`/`-key`.

## What it does

- **CoT relay** (TCP 8087, plus TLS 8089 in the current binary): location, markers and other CoT
  events are forwarded between connected clients. The latest position or marker for each uid is
  kept, so a client that connects later sees everyone right away. Stale entries are dropped.
- **Data packages** (HTTP 8080): upload, search and download through the same `/Marti/sync/...`
  endpoints a TAK Server exposes. Files are stored under `/data/local/tmp/tak/data`.
  `http://192.168.100.1:8080/` is a small web page that lists packages and lets you upload from a browser.
- **Auto-start on boot**, using the puck's `himiwebserver` service (see below).

There is **no authentication** on plain TCP/HTTP. Anyone on the puck's WiFi can read and upload
packages, so the WiFi password is the only gate. The server listens on the WiFi bridge address only,
not on the cellular side.

## Install

On a Mac/Linux machine with `adb` and the puck plugged in:

    ./deploy.sh              # binds to 192.168.100.1 (the puck's WiFi bridge)

`deploy.sh` pushes `bin/puck-tak-armv7`, generates `cert.pem`/`key.pem` if you don't have them
(they are git-ignored), sets up auto-start, starts the server and prints the log. You should see a
line starting `puck-tak ... up`.

Quick check: connect a phone to the puck's WiFi and open `http://192.168.100.1:8080/`.

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

At boot init reads `local.prop`, starts "himiwebserver", and that starts puck-tak. `deploy.sh` does
all three steps. Verified to survive a reboot.

To undo:

    adb remount
    adb shell 'rm /system/bin/himiwebserver && mv /system/bin/himiwebserver.real /system/bin/himiwebserver'
    adb shell 'rm /data/local.prop'

`scripts/legacy/` keeps the earlier init.d and boot-script attempts for reference; they don't run on this device.

## Everyday commands

    adb shell cat /data/local/tmp/tak/server.log                 # what happened
    adb shell 'kill $(cat /data/local/tmp/tak/pid)'              # stop
    adb shell /data/local/tmp/himiwebserver-wrapper.sh           # start (same as at boot)

The log rotates at 1 MB (one old file is kept). Add `-v` to log every CoT event while debugging.

Options: `-cot-port`, `-http-port`, `-max-upload-mb` (default 100), `-max-store-mb` (default 500),
`-bind`, `-data`, `-log`, `-pidfile`, `-daemon`, `-v`; current binary also has `-cert`, `-key`, `-video-multicast`.

## Build from source

    CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "-s -w" -o bin/puck-tak-armv7 .
    go build -o puck-tak . && python3 test_e2e.py ./puck-tak /tmp/pucktest     # 37 checks

## Still to do

- Get the TLS/video source changes back into this repo.
- Turn off the unauthenticated network adb listener (port 22345) on the puck.
- Client-certificate auth for TLS.

See `docs/NOTES.md` for hardware details and what was tried along the way.
