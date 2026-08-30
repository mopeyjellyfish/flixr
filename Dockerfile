# syntax=docker/dockerfile:1
FROM node:22-alpine AS frontend-build
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json frontend/
RUN npm --prefix frontend ci
COPY frontend frontend
RUN npm --prefix frontend run build

FROM golang:1.25-alpine AS backend-build
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend ./
COPY --from=frontend-build /src/frontend/dist ./web/assets
# modernc.org/sqlite is a pure-Go driver, so this executable needs no C toolchain.
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/flixr .

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates ffmpeg \
    && addgroup -S flixr \
    && adduser -S -G flixr -h /data flixr \
    && mkdir -p /data \
    && chown flixr:flixr /data
COPY --from=backend-build /out/flixr /usr/local/bin/flixr
USER flixr:flixr
ENV FLIXR_DATA_DIR=/data
EXPOSE 8787
ENTRYPOINT ["/usr/local/bin/flixr"]
