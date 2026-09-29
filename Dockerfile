# The cortex-daemon in a container: loopback-only inside the container is
# meaningless, so bind 0.0.0.0 on the container edge and ALWAYS run with
# -auth-token; publish the port only to 127.0.0.1 on the host:
#   docker run -p 127.0.0.1:9911:9911 -e CORTEX_TOKEN=... tpt-cortex-daemon \
#     -auth-token "$CORTEX_TOKEN"
# Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY cortex-daemon/go.mod cortex-daemon/go.sum ./
RUN go mod download
COPY cortex-daemon/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/cortex-daemon ./cmd/cortex-daemon

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cortex-daemon /cortex-daemon
VOLUME /data
# The daemon is flag-driven by design (no env indirection): the CMD passes
# the container paths explicitly.
ENTRYPOINT ["/cortex-daemon"]
CMD ["-addr", "0.0.0.0:9911", "-queue", "/data/queue.json", "-data-dir", "/data"]
