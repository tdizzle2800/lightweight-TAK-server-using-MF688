#!/bin/bash
# Push puck-tak to the puck over adb and start it.
# Usage: ./deploy.sh [bind-address]      (default bind address: 192.168.100.1)
set -e
cd "$(dirname "$0")"

BIN=${BIN:-bin/puck-tak-armv7}

# TLS cert/key are not kept in git. Make a self-signed pair if none is here.
if [ ! -f cert.pem ] || [ ! -f key.pem ]; then
  echo "No cert.pem/key.pem found, generating a self-signed pair..."
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
    -keyout key.pem -out cert.pem -subj "/CN=192.168.100.1" >/dev/null 2>&1
fi
BIND=${1:-192.168.100.1}
DIR=/data/local/tmp/tak

command -v adb >/dev/null || { echo "adb not found in PATH"; exit 1; }
[ -f "$BIN" ] || { echo "Put $BIN in the same folder as this script"; exit 1; }
adb get-state >/dev/null || { echo "No adb device found"; exit 1; }

# stop an older copy if one is running
adb shell "[ -f $DIR/pid ] && kill \$(cat $DIR/pid)" >/dev/null 2>&1 || true
sleep 1

adb shell "mkdir -p $DIR/data"
adb push "$BIN" "$DIR/puck-tak"
adb push "cert.pem" "$DIR/cert.pem"
adb push "key.pem" "$DIR/key.pem"
adb shell "chmod 755 $DIR/puck-tak"

# Setup auto-boot via himiwebserver property hijack
echo "Setting up auto-boot..."

# Create wrapper script that starts puck-tak
WRAPPER="/tmp/himi-wrapper.sh"
cat > "$WRAPPER" << 'WRAPPER_EOF'
#!/system/bin/sh
DIR="/data/local/tmp/tak"
exec "$DIR/puck-tak" -daemon -bind 192.168.100.1 -data "$DIR/data" -log "$DIR/server.log" -pidfile "$DIR/pid" -cert "$DIR/cert.pem" -key "$DIR/key.pem"
WRAPPER_EOF
chmod 755 "$WRAPPER"

# Push wrapper to puck
adb push "$WRAPPER" "/data/local/tmp/himiwebserver-wrapper.sh"
adb shell "chmod 755 /data/local/tmp/himiwebserver-wrapper.sh"

# Create boot property file to trigger himiwebserver
adb shell "echo sys.himiweb_start=1 > /data/local.prop"

# Remount system as writable and replace himiwebserver with symlink
adb remount >/dev/null 2>&1 || true
adb shell "mv /system/bin/himiwebserver /system/bin/himiwebserver.real 2>/dev/null || true"
adb shell "ln -s /data/local/tmp/himiwebserver-wrapper.sh /system/bin/himiwebserver"

# Verify setup
adb shell "ls -la /system/bin/himiwebserver"
adb shell "cat /data/local.prop"

adb shell "$DIR/puck-tak -version"
adb shell "$DIR/puck-tak -daemon -bind $BIND -data $DIR/data -log $DIR/server.log -pidfile $DIR/pid -cert $DIR/cert.pem -key $DIR/key.pem"
sleep 2

echo
echo "--- server log ---"
adb shell "cat $DIR/server.log"
echo
echo "✓ puck-tak is running and WILL AUTO-START on reboot"
echo "Clients: TAK server $BIND, plain TCP port 8087 or TLS port 8089.  Web page: http://$BIND:8080/"
echo ""
echo "Boot mechanism: /data/local.prop sets sys.himiweb_start=1 → init starts himiwebserver → symlink to wrapper → starts puck-tak"
