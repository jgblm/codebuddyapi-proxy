# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26.4-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
ARG COMMIT=
ARG BUILT_AT=
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath \
      -ldflags="-s -w -X github.com/wnddd839/codebuddy-proxy/internal/version.Version=${VERSION} -X github.com/wnddd839/codebuddy-proxy/internal/version.Commit=${COMMIT} -X github.com/wnddd839/codebuddy-proxy/internal/version.BuiltAt=${BUILT_AT}" \
      -o /out/codebuddy-proxy ./cmd/codebuddy-proxy

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 buddy \
    && adduser -S -D -H -u 10001 -G buddy buddy \
    && mkdir -p /data \
    && chown buddy:buddy /data
COPY --from=build /out/codebuddy-proxy /usr/local/bin/codebuddy-proxy
COPY LICENSE /usr/share/licenses/buddy-proxy/LICENSE
ENV HOME=/data \
    CODEBUDDY_PROXY_CONTAINER=1 \
    CODEBUDDY_PROXY_HOST=0.0.0.0 \
    CODEBUDDY_PROXY_PORT=32126 \
    CODEBUDDY_PROXY_ENV_FILE=/data/proxy.env \
    CODEBUDDY_PROXY_ACCOUNTS_PATH=/data/proxy-accounts.json \
    CODEBUDDY_PROXY_USAGE_PATH=/data/proxy-usage.json
WORKDIR /data
USER 10001:10001
EXPOSE 32126
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -T 3 -O /dev/null "http://127.0.0.1:${CODEBUDDY_PROXY_PORT}/health" || exit 1
ENTRYPOINT ["/usr/local/bin/codebuddy-proxy"]
