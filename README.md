# server-monitor

Monitor de sistema embebible en CasaOS para `jordisama-server` (Ubuntu 24.04, AMD Ryzen 5 3500U + Vega 8). Expone un dashboard web con métricas en vivo del host: CPU, memoria, disco, red, batería, sensores y contenedores Docker.

> **Estado:** en desarrollo (Fase 0 — esqueleto del proyecto).

## Stack

- **Lenguaje:** Go 1.24
- **HTTP:** `net/http` + `chi`
- **Frontend:** HTML + JS vanilla + Chart.js (servido vía `embed.FS`)
- **Streaming:** SSE (Server-Sent Events) con fallback a polling
- **Empaquetado:** Docker multi-stage, contenedor `<30 MB`, instalable como Custom App en CasaOS

## Estructura del repo

```
server-monitor/
├── README.md
├── run                        # Facade: ./run build|test|lint|check|run|clean
├── .gitignore
└── src/                       # Módulo Go (go.mod vive aquí)
    ├── go.mod
    ├── .golangci.yml
    ├── cmd/server/main.go     # Entrypoint
    ├── pkg/                   # 95% del código (collectors, api, model)
    ├── internal/              # Solo si algo NO debe ser importable
    └── web/                   # HTML/CSS/JS embebido
```

## Uso

```bash
./run build       # Compila a output/bin/server
./run test        # go test ./... con logs a output/testing/
./run lint        # gofmt + go vet + golangci-lint
./run check       # Quality gate completo (lint + tests)
./run run         # Compila y ejecuta
./run clean       # Limpia output/
```

También está disponible como `server-monitor` global vía `~/.local/bin/server-monitor`.

## Configuración (env vars)

Todas las opciones se configuran por variables de entorno. Defaults pensados para correr en el container Docker dentro de CasaOS.

| Variable | Default | Descripción |
|---|---|---|
| `PORT` | `8080` | Puerto HTTP del dashboard |
| `REFRESH_INTERVAL_SECONDS` | `2` | Intervalo de refresco de métricas en SSE |
| `SENSORS_CACHE_SECONDS` | `1` | TTL del caché de `sensors -j` |
| `SMARTCTL_CACHE_SECONDS` | `60` | TTL del caché de `smartctl` (evita gastar SMART) |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` |
| `HOST_PROC` | `/proc` | Path a `/proc` del host (`/host/proc` en container) |
| `HOST_SYS` | `/sys` | Path a `/sys` del host (`/host/sys` en container) |
| `DEVICE_SSD` | `/dev/sdb` | Dispositivo SSD para `smartctl` |
| `DEVICE_HDD` | `/dev/sda` | Dispositivo HDD para `smartctl` |
| `BATTERY_NAME` | `BAT1` | Nombre de batería bajo `/sys/class/power_supply/` |
| `GPU_CARD` | `card1` | Tarjeta GPU bajo `/sys/class/drm/` |
| `THRESHOLD_CPU_TEMP_WARN` | `70` | Umbral amarillo para temp CPU (°C) |
| `THRESHOLD_CPU_TEMP_CRIT` | `80` | Umbral rojo para temp CPU (°C) |
| `THRESHOLD_GPU_TEMP_WARN` | `75` | Umbral amarillo para temp GPU (°C) |
| `THRESHOLD_GPU_TEMP_CRIT` | `90` | Umbral rojo para temp GPU (°C) — Vega 8 tolera hasta 95°C |
| `THRESHOLD_DISK_TEMP_WARN` | `45` | Umbral amarillo para temp disco (°C) |
| `THRESHOLD_DISK_TEMP_CRIT` | `55` | Umbral rojo para temp disco (°C) |
| `THRESHOLD_CPU_USAGE_WARN` | `60` | Umbral amarillo para uso CPU (%) |
| `THRESHOLD_CPU_USAGE_CRIT` | `85` | Umbral rojo para uso CPU (%) |
| `THRESHOLD_MEM_USAGE_WARN` | `70` | Umbral amarillo para uso RAM (%) |
| `THRESHOLD_MEM_USAGE_CRIT` | `90` | Umbral rojo para uso RAM (%) |
| `THRESHOLD_DISK_USAGE_WARN` | `70` | Umbral amarillo para uso disco (%) |
| `THRESHOLD_DISK_USAGE_CRIT` | `85` | Umbral rojo para uso disco (%) |
| `THRESHOLD_BATTERY_WARN` | `50` | Umbral amarillo de batería (% restante en descarga) |
| `THRESHOLD_BATTERY_CRIT` | `20` | Umbral rojo de batería |

## Despliegue en CasaOS

> Pendiente — Fase 6.

## Licencia

Uso personal. Sin licencia pública aún.
