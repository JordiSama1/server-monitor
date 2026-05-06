# server-monitor

Real-time system dashboard for self-hosted Linux servers. Built with Go — single binary, no runtime dependencies, embeds the full web UI. Designed to run as a Docker container on [CasaOS](https://casaos.io).

## Features

- **Live metrics via SSE** — CPU (per-core usage, temp, freq), RAM, disk, network, GPU, battery, Docker containers, top processes by RAM
- **Telegram alerts** — threshold-based notifications (warn/crit) with configurable cooldown and daily status digest
- **Kill processes** — terminate processes directly from the dashboard with confirmation modal
- **CasaOS ready** — `docker-compose.yml` with `x-casaos` metadata for one-click custom app install
- **Exponential backoff** — SSE reconnect with polling fallback after 3 failures

## Quick start

```bash
docker compose up -d
```

Open `http://<your-server>:8080`.

## Configuration

All options are environment variables. Defaults work out of the box for most setups.

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | Dashboard HTTP port |
| `REFRESH_INTERVAL_SECONDS` | `2` | SSE push interval |
| `HOST_PROC` | `/proc` | Host `/proc` path |
| `HOST_SYS` | `/sys` | Host `/sys` path |
| `BATTERY_NAME` | `BAT1` | Battery under `/sys/class/power_supply/` |
| `GPU_CARD` | `card1` | GPU card under `/sys/class/drm/` |
| `GPU_NAME` | `AMD Vega 8` | GPU display name |
| `DEVICE_SSD` | `/dev/sdb` | Primary disk for smartctl |
| `DEVICE_HDD` | `/dev/sda` | Secondary disk for smartctl |
| `NETWORK_INTERFACES` | `enp1s0f1,tailscale0,docker0,wlp2s0` | Interfaces to monitor |
| `DASHBOARD_URL` | `http://100.94.124.107:8080` | URL shown in Telegram digest |
| `TIMEZONE` | `America/Santiago` | Timezone for digest timestamp |
| `TELEGRAM_BOT_TOKEN` | — | Telegram bot token (alerts disabled if empty) |
| `TELEGRAM_CHAT_ID` | — | Telegram chat ID to receive alerts |
| `ALERT_COOLDOWN_SECONDS` | `1800` | Min seconds between repeated alerts |
| `DIGEST_HOUR` | `10` | Local hour to send daily digest (0–23) |
| `SMARTCTL_CACHE_SECONDS` | `60` | smartctl result TTL |
| `SENSORS_CACHE_SECONDS` | `1` | lm-sensors result TTL |

### Thresholds

| Variable | Default | Description |
|---|---|---|
| `THRESHOLD_CPU_TEMP_WARN` / `_CRIT` | `70` / `80` | CPU temp (°C) |
| `THRESHOLD_GPU_TEMP_WARN` / `_CRIT` | `75` / `90` | GPU temp (°C) |
| `THRESHOLD_DISK_TEMP_WARN` / `_CRIT` | `45` / `55` | Disk temp (°C) |
| `THRESHOLD_CPU_USAGE_WARN` / `_CRIT` | `60` / `85` | CPU usage (%) |
| `THRESHOLD_MEM_USAGE_WARN` / `_CRIT` | `70` / `90` | RAM usage (%) |
| `THRESHOLD_DISK_USAGE_WARN` / `_CRIT` | `70` / `85` | Disk usage (%) |
| `THRESHOLD_BATTERY_WARN` / `_CRIT` | `50` / `20` | Battery level (%) |

## Telegram bot commands

| Command | Description |
|---|---|
| `/status` | Sends an immediate status digest |

## Stack

- **Go 1.26** — single binary with embedded assets (`embed.FS`)
- **chi** — HTTP router
- **SSE** — real-time streaming with polling fallback
- **Vanilla JS** — no frameworks, DOM-only (no `innerHTML`)
- **Docker** — `debian:bookworm-slim` + `smartmontools` + `lm-sensors`

## Project structure

```
server-monitor/
├── Dockerfile
├── docker-compose.yml
├── .env                        # local credentials (gitignored)
└── src/
    ├── cmd/server/main.go      # entrypoint
    └── internal/
        ├── alerts/             # Telegram alerts + daily digest
        ├── api/                # HTTP server + SSE + kill endpoint
        ├── collector/          # 9 metric collectors
        ├── config/             # env var config
        ├── model/              # wire format types
        └── web/                # embedded HTML/CSS/JS
```

## Building locally

```bash
cd src
go build -o ../server-monitor ./cmd/server
docker build -t server-monitor:latest ..
docker compose up -d --force-recreate
```

## License

MIT
