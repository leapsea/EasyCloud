# Base images can be overridden, e.g. to use a local mirror:
#   docker build --build-arg NODE_IMAGE=... --build-arg GO_IMAGE=... --build-arg BASE_IMAGE=...
# Package mirrors (optional; empty = upstream defaults):
#   --build-arg NPM_REGISTRY=https://registry.npmmirror.com --build-arg GOPROXY=https://goproxy.cn,direct
ARG NODE_IMAGE=node:22-alpine
ARG GO_IMAGE=golang:1.26-alpine
ARG BASE_IMAGE=alpine:3.22
# Where the binary comes from: "build" compiles it in the stages below (local
# builds, `make docker`); "prebuilt" takes .ci-bin/linux-<arch>/easycloud from the
# context, compiled beforehand on the CI runner with its Go build cache. BuildKit
# skips the stages the chosen one does not need.
ARG BINARY_FROM=build

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web
WORKDIR /src/web
ARG NPM_REGISTRY=
COPY web/package.json web/package-lock.json* ./
# npm rewrites registry.npmjs.org in the lockfile to the configured registry.
RUN if [ -n "$NPM_REGISTRY" ]; then npm config set registry "$NPM_REGISTRY"; fi && npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS TARGETARCH VERSION=dev
# An empty GOPROXY makes Go fall back to its default proxy.
ARG GOPROXY=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/easycloud ./cmd/easycloud

FROM scratch AS prebuilt
ARG TARGETOS TARGETARCH
COPY .ci-bin/${TARGETOS}-${TARGETARCH}/easycloud /out/easycloud

FROM ${BINARY_FROM} AS binary

FROM ${BASE_IMAGE}
# ffmpeg lets the ffmpeg-thumbnail plugin work out of the box.
RUN apk add --no-cache ca-certificates tzdata su-exec ffmpeg
COPY --from=binary /out/easycloud /usr/local/bin/easycloud
COPY deploy/docker/entrypoint.sh /entrypoint.sh
# /logs is deliberately not a VOLUME: log files are written only when you mount it.
# Port 80 also works for a non-root `user:` because Docker lets containers bind
# low ports (net.ipv4.ip_unprivileged_port_start=0); with network_mode: host set
# EASYCLOUD_HTTP_LISTEN to a port above 1024 instead.
ENV EASYCLOUD_DATA_ROOT=/data \
    EASYCLOUD_APP_DATA=/config \
    EASYCLOUD_LOG_DIR=/logs \
    EASYCLOUD_HTTP_LISTEN=:80 \
    PUID=1000 PGID=1000
VOLUME ["/data", "/config"]
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- "http://127.0.0.1:${EASYCLOUD_HTTP_LISTEN##*:}/healthz" >/dev/null || exit 1
ENTRYPOINT ["/entrypoint.sh"]
CMD ["serve"]
