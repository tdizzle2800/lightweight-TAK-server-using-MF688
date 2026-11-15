#!/bin/bash
# Install puck-tak on an MF688 puck over adb (macOS / Linux).
#
#   ./deploy.sh                    install or update, bind to 192.168.100.1
#   ./deploy.sh --bind 10.0.0.1    use a different WiFi address
#   ./deploy.sh --no-autostart     install and start, but don't touch /system
#   ./deploy.sh --uninstall        stop it and undo the auto-start changes (keeps packages)
#   ./deploy.sh --uninstall --purge   ... and delete all data packages and the TLS cert
#
# Windows: use deploy.cmd / deploy.ps1 instead.
set -e
cd "$(dirname "$0")"

BIND=192.168.100.1
MODE=install
AUTOSTART=1
PURGE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --bind) BIND="$2"; shift 2 ;;
    --no-autostart) AUTOSTART=0; shift ;;
    --uninstall) MODE=uninstall; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "Unknown option: $1 (try --help)"; exit 1 ;;
  esac
done

BIN=bin/puck-tak-armv7
DIR=/data/local/tmp/tak
WRAP=/data/local/tmp/himiwebserver-wrapper.sh
HIMI=/system/bin/himiwebserver
PROP=/data/local.prop
PROPLINE=sys.himiweb_start=1

say()  { printf '\n==> %s\n' "$*"; }
warn() { printf '  ! %s\n' "$*"; }
die()  { printf '\nERROR: %s\n' "$*" >&2; exit 1; }

# find adb: PATH, a platform-tools folder next to this script, or the Android SDK
ADB=$(command -v adb || true)
for c in ./platform-tools/adb "$HOME/Library/Android/sdk/platform-tools/adb" "$HOME/Android/Sdk/platform-tools/adb"; do
  [ -z "$ADB" ] && [ -x "$c" ] && ADB=$c
done
[ -n "$ADB" ] || die "adb not found. Install Android platform-tools (see INSTALL.md) or unzip them into ./platform-tools"

# old adbd does not pass exit codes back, so every check reads output instead
sh_out() { "$ADB" shell "$1" | tr -d '\r'; }
push()   { local e; e=$("$ADB" push "$1" "$2" 2>&1) || die "copying $1 to the puck failed: $e"; }
exists() { [ "$(sh_out "if [ -e $1 ]; then echo yes; else echo no; fi")" = yes ]; }

say "Checking the puck"
STATE=$("$ADB" get-state 2>&1 | tr -d '\r' || true)
case "$STATE" in
  device) ;;
  *unauthorized*) die "the puck is connected but not authorized for adb" ;;
  *more\ than\ one*) die "more than one adb device is connected; unplug the others" ;;
  *) die "no adb device found. Is the puck plugged in, and does 'adb devices' list it?" ;;
esac
echo "  model: $(sh_out 'getprop ro.product.model')  Android $(sh_out 'getprop ro.build.version.release')"
[ "$(sh_out id | cut -c1-5)" = "uid=0" ] || warn "adb shell is not root here; installing to /system will fail (use --no-autostart)"

stop_server() {
  "$ADB" shell "if [ -f $DIR/pid ]; then kill \$(cat $DIR/pid); rm $DIR/pid; fi" >/dev/null 2>&1 || true
  sleep 1
}

if [ "$MODE" = uninstall ]; then
  say "Stopping puck-tak"
  stop_server
  if exists "$HIMI.real"; then
    say "Restoring the stock himiwebserver"
    "$ADB" remount >/dev/null 2>&1 || true
    "$ADB" shell "rm $HIMI; mv $HIMI.real $HIMI" >/dev/null 2>&1
    exists "$HIMI.real" && warn "could not restore $HIMI (is /system writable?)" || echo "  restored"
  fi
  if exists "$PROP"; then
    if [ "$(sh_out "cat $PROP")" = "$PROPLINE" ]; then
      "$ADB" shell "rm $PROP"; echo "  removed $PROP"
    else
      warn "$PROP has other settings too; remove the line '$PROPLINE' from it yourself"
    fi
  fi
  "$ADB" shell "rm $WRAP" >/dev/null 2>&1 || true
  if [ "$PURGE" = 1 ]; then
    "$ADB" shell "rm -r $DIR" >/dev/null 2>&1 || true
    echo "  deleted $DIR (packages, log, cert)"
  else
    echo "  kept $DIR (packages, log, cert). Use --uninstall --purge to delete it."
  fi
  say "Uninstalled. Reboot the puck to be sure the stock web server is back."
  exit 0
fi

[ -f "$BIN" ] || die "$BIN is missing. Download the whole repository, not just this script."

say "Installing puck-tak"
stop_server
"$ADB" shell "mkdir -p $DIR/data"
push "$BIN" "$DIR/puck-tak"
"$ADB" shell "chmod 755 $DIR/puck-tak"
echo "  $(sh_out "$DIR/puck-tak -version")"
if [ -f cert.pem ] && [ -f key.pem ]; then
  push cert.pem "$DIR/cert.pem"
  push key.pem "$DIR/key.pem"
  echo "  using cert.pem/key.pem from this folder"
else
  echo "  the puck will create its own TLS certificate on first start (or keep the one it has)"
fi

# boot wrapper, with LF line endings and the chosen bind address
TMPW=$(mktemp)
tr -d '\r' < scripts/himi-wrapper.sh | sed "s/-bind 192\.168\.100\.1/-bind $BIND/" > "$TMPW"
push "$TMPW" "$WRAP"
rm -f "$TMPW"
"$ADB" shell "chmod 755 $WRAP"

if [ "$AUTOSTART" = 1 ]; then
  say "Setting up auto-start at boot"
  if ! exists "$HIMI" && ! exists "$HIMI.real"; then
    warn "$HIMI is not on this firmware, so the auto-start trick won't work here."
    warn "puck-tak will run now, but you'll need to start it again after a reboot."
  else
    "$ADB" remount >/dev/null 2>&1 || true
    if ! exists "$HIMI.real"; then
      "$ADB" shell "mv $HIMI $HIMI.real" >/dev/null 2>&1
    fi
    "$ADB" shell "rm $HIMI 2>/dev/null; ln -s $WRAP $HIMI" >/dev/null 2>&1
    if exists "$PROP" && sh_out "cat $PROP" | grep -qx "$PROPLINE"; then
      :
    else
      "$ADB" shell "echo $PROPLINE >> $PROP; chmod 644 $PROP"
    fi
    if sh_out "ls -l $HIMI" | grep -q himiwebserver-wrapper; then
      echo "  ok: $HIMI -> $WRAP, $PROP sets $PROPLINE"
    else
      warn "could not replace $HIMI (is /system read-only?). Auto-start is NOT set up."
    fi
  fi
fi

say "Starting puck-tak"
BEFORE=$(sh_out "cat $DIR/server.log 2>/dev/null" | wc -l)
"$ADB" shell "sh $WRAP" >/dev/null 2>&1
sleep 4   # first start creates the TLS certificate
NEW=$(sh_out "cat $DIR/server.log" | tail -n +$((BEFORE + 1)))
echo "$NEW" | tail -n 8 | sed 's/^/  /'
echo "$NEW" | grep -q " up: " || die "puck-tak did not report 'up'. The log above should say why."

cat <<EOF

==> Done. puck-tak is running.
    Web page (packages):  http://$BIND:8080/
    TAK server address:   $BIND
      plain:  port 8087, protocol TCP
      TLS:    port 8089, protocol SSL  (self-signed cert, see INSTALL.md)
EOF
