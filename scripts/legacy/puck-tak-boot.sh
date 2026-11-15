#!/system/bin/sh
DIR="/data/local/tmp/tak"
LOG="$DIR/server.log"
PID_FILE="$DIR/pid"
sleep 5
exec "$DIR/puck-tak" -daemon -bind 192.168.100.1 -data "$DIR/data" -log "$LOG" -pidfile "$PID_FILE" -cert "$DIR/cert.pem" -key "$DIR/key.pem" >> "$LOG" 2>&1
