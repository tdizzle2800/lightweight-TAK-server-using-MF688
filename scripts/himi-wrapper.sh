#!/system/bin/sh
DIR="/data/local/tmp/tak"
exec "$DIR/puck-tak" -daemon -bind 192.168.100.1 -data "$DIR/data" -log "$DIR/server.log" -pidfile "$DIR/pid" -cert "$DIR/cert.pem" -key "$DIR/key.pem"
