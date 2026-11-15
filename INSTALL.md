# Installing puck-tak

This guide takes you from a stock ZTE MF688 puck to a working off-grid TAK server. It takes about
15 minutes and works from **Windows, macOS or Linux**. You don't need to build anything: the server
binary is already in `bin/`.

> **Read this first**
> - This has been tested on **one** MF688 (Android 4.4.4, `userdebug` build, where `adb shell` runs
>   as root). Other firmware versions may not give root over adb or may lack the `himiwebserver`
>   service used for auto-start. The installer checks for both and tells you if something is missing.
> - Auto-start changes `/system` on the puck. `deploy -Uninstall` / `--uninstall` puts it back.
> - There are **no user accounts**. Anyone who can join the puck's WiFi can see positions and
>   download or upload data packages. Use a strong WiFi password. (The stock firmware also allows
>   password-free adb over WiFi on port 22345, so this matters even without puck-tak.)
> - Provided as is, with no warranty (see LICENSE). You are modifying your own device at your own risk.

## What you need

| Item | Notes |
|------|-------|
| ZTE MF688 puck | Charged, powered on |
| USB cable | One that carries data, not just power |
| Computer | Windows 10/11, macOS or Linux |
| Phone or tablet with a TAK app | ATAK-CIV (Android) is what this was tested with; iTAK and WinTAK may work |

## 1. Install adb (Android platform-tools)

**Windows**

1. Download *SDK Platform-Tools for Windows* from
   <https://developer.android.com/tools/releases/platform-tools>.
2. Unzip it. You get a folder called `platform-tools`.
3. Move that whole `platform-tools` folder **into the puck-tak folder** (next to `deploy.cmd`).
   The installer finds it there, so you don't have to touch PATH.

**macOS**: `brew install android-platform-tools`, or download the macOS zip from the link above and
put the `platform-tools` folder next to `deploy.sh`.

**Linux**: `sudo apt install adb` (Debian/Ubuntu), `sudo dnf install android-tools` (Fedora), or the
Linux zip from the link above.

## 2. Get puck-tak

On the GitHub page: **Code → Download ZIP**, then unzip it. If you use git:
`git clone https://github.com/tdizzle2800/lightweight-TAK-server-using-MF688.git`.

(While the repository is private, only people you've added on GitHub can download it.)

## 3. Connect the puck

Plug the puck into the computer and check that adb sees it.

Windows (in the puck-tak folder, open a Command Prompt or PowerShell there):

    platform-tools\adb devices

macOS / Linux:

    adb devices        # or ./platform-tools/adb devices

You want one line ending in `device`:

    List of devices attached
    0123456789ABCDEF    device

If the list is empty:

- Try another cable or USB port; many cables are charge-only.
- **Windows** may need a USB driver. Open Device Manager. If the puck shows up with a yellow warning
  sign, right-click it → *Update driver* → *Browse my computer* → *Let me pick* → *Android Device* →
  *Android ADB Interface*. If that option isn't there, install the *Google USB Driver* first
  (<https://developer.android.com/studio/run/win-usb>).
- **Linux** may need udev rules. Running `adb` with `sudo` once is a quick way to check whether
  that's the problem.

If it says `unauthorized`, adb is blocked on this firmware, and puck-tak can't be installed this way.

## 4. Run the installer

**Windows**: double-click **`deploy.cmd`**. A window opens, runs the install and waits for a key press
at the end.

**macOS / Linux**: in a terminal in the puck-tak folder:

    bash deploy.sh

The installer:

1. Checks that the puck is connected and that adb runs as root.
2. Copies the server to `/data/local/tmp/tak/` on the puck.
3. Sets up auto-start at boot (see README, *Auto-start on boot*).
4. Starts the server and shows its log.

A good run ends like this:

    ==> Done. puck-tak is running.
        Web page (packages):  http://192.168.100.1:8080/
        TAK server address:   192.168.100.1
          plain:  port 8087, protocol TCP
          TLS:    port 8089, protocol SSL  (self-signed cert, see INSTALL.md)

The first start takes a few extra seconds while the puck makes its own TLS certificate.

You can unplug the puck now. It keeps running and comes back by itself after a reboot.

## 5. Check it from a phone

1. Connect the phone to the puck's WiFi.
2. Open `http://192.168.100.1:8080/` in the phone's browser. You should see the puck-tak page with
   an (empty) package list and an upload button.

Android may say the WiFi network has no internet and offer to switch away. Choose to stay connected.

## 6. Connect ATAK

In ATAK open the server settings (*Settings → Network Preferences → TAK Server connections* or
similar; the wording changes between ATAK versions) and add a server:

| Setting | Value |
|---------|-------|
| Name | anything, e.g. `puck` |
| Address | `192.168.100.1` |
| Port | `8087` |
| Protocol | **TCP** (not SSL) |
| Authentication / enrollment | off |

The connection indicator should turn green. Do the same on every device. Each device then sees the
others' positions and markers. A device that joins later gets everyone's last known position
straight away.

**About TLS (port 8089)**: the server also offers an encrypted port with a self-signed
certificate. ATAK only connects to it if it trusts that certificate, which means building a
truststore from the puck's `cert.pem` (`adb pull /data/local/tmp/tak/cert.pem`). That route hasn't
been worked through yet, so **use plain TCP on 8087** unless you know how to set up an ATAK truststore.

## 7. Share data packages

- **From a browser**: open `http://192.168.100.1:8080/`, upload a file, and anyone can download it from
  the same page.
- **From ATAK**: send a data package to the server from ATAK's data-package tool. Other devices can then find
  and download it from the server. Menu names differ by ATAK version.

Packages are kept on the puck (up to 500 MB in total, 100 MB per file) and survive reboots.

## Updating

Download the new version and run the installer again. Your data packages and certificate are kept.

## Uninstalling

| | Keeps packages and cert | Deletes everything |
|---|---|---|
| Windows | `deploy.cmd -Uninstall` | `deploy.cmd -Uninstall -Purge` |
| macOS / Linux | `bash deploy.sh --uninstall` | `bash deploy.sh --uninstall --purge` |

This stops the server and restores the puck's original `himiwebserver`. Reboot the puck afterwards.

## Other options

| Windows | macOS / Linux | What it does |
|---------|---------------|--------------|
| `-Bind 10.0.0.1` | `--bind 10.0.0.1` | Your puck's WiFi uses a different address |
| `-NoAutostart` | `--no-autostart` | Don't change `/system`; you'll start it by hand after each reboot |

To use your own certificate, put `cert.pem` and `key.pem` next to the installer before running it.

## Troubleshooting

| Problem | What to do |
|---------|------------|
| `adb not found` | Put the `platform-tools` folder next to the installer (step 1). |
| `no adb device found` | See step 3. |
| `adb shell is not root here` | This firmware doesn't give root over adb. Try the `no-autostart` option. If that fails too, this puck can't run puck-tak this way. |
| `did not report 'up'`, and the log says `waiting for 192.168.100.1` | The puck's WiFi address is different. Run `adb shell netcfg` and look for the WiFi bridge address (often on `br0` or `wlan0`), then use the `bind` option. |
| `himiwebserver is not on this firmware` | Auto-start won't work on this puck. The server still runs until the next reboot. Run the installer again after rebooting to start it. |
| Phone can't open the web page | Make sure the phone is on the puck's WiFi and not on mobile data. Turn mobile data off for the test. |
| ATAK won't connect or keeps disconnecting | Check that you chose **TCP**, not SSL, on port 8087. Then read the log: `adb shell cat /data/local/tmp/tak/server.log` |
| Not running after a reboot | `adb shell getprop sys.himiweb_start` should print `1`, and `adb shell ls -l /system/bin/himiwebserver` should point to `himiwebserver-wrapper.sh`. Run the installer again if not. |

The server logs every connection and request. When something doesn't work, the log
(`adb shell cat /data/local/tmp/tak/server.log`) almost always says why.
