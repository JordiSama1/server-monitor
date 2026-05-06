# ── Runtime ─────────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

# smartmontools for disk temps, lm-sensors for CPU/GPU temps
RUN apt-get update && apt-get install -y --no-install-recommends \
    smartmontools \
    lm-sensors \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Binary is pre-built locally with: make build  (see Makefile)
COPY server-monitor /usr/local/bin/server-monitor

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/server-monitor"]
