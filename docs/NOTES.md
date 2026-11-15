# Project notes

## Hardware

- ZTE MF688 WiFi puck: Qualcomm MSM8916, 32-bit ARMv7, ~400 MB RAM (low-RAM config, zram swap)
- Android 4.4.4 (SDK 19), userdebug build; `adb shell` runs as uid 0 (root)
- No `uname`, `which` or `df -h` in the shell
- WiFi bridge address: `192.168.100.1`
- An unauthenticated network adb listener is open on port 22345 (should be turned off)

## Why a custom server

Full OpenTAKServer is far too heavy for 400 MB of RAM on Android 4.4. The goal is only passing data
packages and location between TAK clients on the puck's WiFi, so puck-tak is a single static Go binary.

## Auto-start: what was tried

| Attempt                                 | Result                                      |
|-----------------------------------------|---------------------------------------------|
| `/system/etc/init.d/99-puck-tak`        | init.d is not run on this firmware          |
| Add a service to `/init.rc`             | Read-only partition, bootloader is locked   |
| Hijack `himiwebserver` via `local.prop` | ✅ Works, survives reboot                    |

## Video (shelved)

Goal was an Akaso EK7000 helmet cam (UVC over USB) streamed to ATAK through the puck.

- The puck likely can't act as a USB host, so the camera went into an Android phone running ATAK.
- ATAK helmet-cam plugin streamed from the phone. Multicast UDP (239.1.1.1:24613) failed on the
  puck's WiFi interface (`setsockopt` error).
- Switched to RTP on UDP 8554 with a 5 MB rolling buffer served at `/video` and `/stream` (MPEG-TS).
  Clients timed out, and the work was shelved in favor of auto-start.
- Seen once: when iTAK on an iPhone tried to open the stream, ATAK on Android disconnected from the puck.
