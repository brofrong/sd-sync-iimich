# The statistics server (cmd/sd-sync-telemetry). Built from the repository
# root: docker build -f build/telemetry/Dockerfile .
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/sd-sync-telemetry cmd/sd-sync-telemetry
COPY internal/telemetryserver internal/telemetryserver
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sd-sync-telemetry ./cmd/sd-sync-telemetry
# The database's folder, owned by the user the server runs as, so a new
# volume mounted there starts writable.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.source="https://github.com/brofrong/sd-sync-immich" \
      org.opencontainers.image.description="Receives SD Sync Immich's anonymous statistics" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/sd-sync-telemetry /sd-sync-telemetry
COPY --from=build --chown=65532:65532 /out/data /data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD ["/sd-sync-telemetry", "healthcheck"]
ENTRYPOINT ["/sd-sync-telemetry"]
