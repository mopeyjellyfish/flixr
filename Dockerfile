# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:26-alpine@sha256:2d984a15c9b54fd0aeb608b8e0d0d83529eb34d2966db27a1fb4f1edc3d298a3 AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json frontend/
RUN npm --prefix frontend ci
COPY frontend frontend
RUN npm --prefix frontend run build

FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS backend-build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend ./
COPY --from=frontend-build /src/frontend/dist ./web/assets
# modernc.org/sqlite is a pure-Go driver, so this executable needs no C toolchain.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG REVISION=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X main.version=$VERSION -X main.revision=$REVISION" -o /out/flixr .

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS runtime
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates ffmpeg \
    && addgroup -S -g 101 flixr \
    && adduser -S -u 100 -G flixr -h /data flixr \
    && mkdir -p /data /config /cache /media \
    && chown flixr:flixr /data /config /cache
COPY --from=backend-build /out/flixr /usr/local/bin/flixr
COPY LICENSE /usr/share/licenses/flixr/LICENSE
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.source="https://github.com/mopeyjellyfish/flixr" \
      org.opencontainers.image.description="Local-first movie and TV media server" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION \
      org.opencontainers.image.licenses="MIT"
USER 100:101
ENV FLIXR_DATA_DIR=/config FLIXR_SEGMENT_DIR=/cache/segments FLIXR_LISTEN_ADDR=0.0.0.0:8787
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8787/api/v1/setup/status || exit 1
EXPOSE 8787
ENTRYPOINT ["/usr/local/bin/flixr"]
