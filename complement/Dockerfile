# syntax=docker/dockerfile:1.7

FROM golang:1.26.1-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/thaumaste .


FROM alpine:3.21 AS runtime

ENV PGDATA=/var/lib/thaumaste/pgdata

RUN apk add --no-cache postgresql17 valkey su-exec ca-certificates \
 && mkdir -p "$PGDATA" /run/postgresql \
 && chown -R postgres:postgres "$PGDATA" /run/postgresql

RUN su-exec postgres initdb --locale=C --encoding=UTF-8 --auth-local=trust --auth-host=trust \
 && su-exec postgres pg_ctl -D "$PGDATA" -w -o "-c listen_addresses=127.0.0.1" start \
 && su-exec postgres createuser thaumaste \
 && su-exec postgres createdb -O thaumaste thaumaste \
 && su-exec postgres pg_ctl -D "$PGDATA" -w -m fast stop

ENV THAUMASTE_POSTGRES_HOST=127.0.0.1 \
    THAUMASTE_POSTGRES_PORT=5432 \
    THAUMASTE_POSTGRES_USER=thaumaste \
    THAUMASTE_POSTGRES_PASSWORD=thaumaste \
    THAUMASTE_POSTGRES_DB=thaumaste \
    THAUMASTE_VALKEY_ADDRS=127.0.0.1:6379 \
    THAUMASTE_SERVER_ADDR=:8008 \
    THAUMASTE_SERVER_PUBLIC_SCHEME=http \
    THAUMASTE_SIGNING_MASTER_KEY=Y29tcGxlbWVudC10ZXN0LW1hc3Rlci1rZXktMzJieXQ= \
    THAUMASTE_HEALTH_DRAIN_DELAY=0s \
    THAUMASTE_LOG_FORMAT=text \
    THAUMASTE_LOG_LEVEL=debug

COPY --from=builder /out/thaumaste /usr/local/bin/thaumaste
COPY complement/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

EXPOSE 8008 8448

HEALTHCHECK --interval=1s --timeout=3s --start-period=2s --retries=30 \
    CMD wget -qO- http://127.0.0.1:8008/_matrix/client/versions || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
