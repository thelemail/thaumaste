# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.1-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/thaumaste .


FROM alpine:3.21 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
 && addgroup -S -g 10001 thaumaste \
 && adduser -S -u 10001 -G thaumaste -h /opt/thaumaste thaumaste

COPY --from=builder /out/thaumaste /usr/local/bin/thaumaste

USER thaumaste
WORKDIR /opt/thaumaste

ENTRYPOINT ["/usr/local/bin/thaumaste"]
