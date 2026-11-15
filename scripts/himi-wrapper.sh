#!/system/bin/sh
# Started by init at boot in place of the stock himiwebserver (see README, "Auto-start on boot").
# The deploy scripts copy this to /data/local/tmp/himiwebserver-wrapper.sh.
DIR=/data/local/tmp/tak
exec "$DIR/puck-tak" -daemon -bind 192.168.100.1 -data "$DIR/data" -log "$DIR/server.log" -pidfile "$DIR/pid" -cert "$DIR/cert.pem" -key "$DIR/key.pem" -gen-cert
