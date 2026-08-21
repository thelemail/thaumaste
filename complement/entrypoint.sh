#!/bin/sh

su-exec postgres pg_ctl -D "$PGDATA" -w -o "-c listen_addresses=127.0.0.1" start

/usr/local/bin/thaumaste migrate up || exit 1
/usr/local/bin/thaumaste tenant ensure "$SERVER_NAME" localhost 127.0.0.1 || exit 1
/usr/local/bin/thaumaste tenant registration "$SERVER_NAME" open || exit 1

/usr/local/bin/thaumaste serve &
server=$!

trap 'kill -TERM "$server" 2>/dev/null' TERM INT

wait "$server"
code=$?
while [ "$code" -gt 128 ] && kill -0 "$server" 2>/dev/null; do
	wait "$server"
	code=$?
done

su-exec postgres pg_ctl -D "$PGDATA" -w -m fast stop

exit "$code"
