// Package model define los tipos del wire-format JSON producidos por el
// proceso server-monitor. Estos tipos forman el contrato consumido por el
// frontend del dashboard y por cualquier integración externa.
//
// NOTA DE CONTRATO: Los structs de este paquete NO aceptan valores no
// finitos (NaN, +Inf, -Inf) en campos float64. La sanitización de las
// lecturas de hardware inválidas es responsabilidad del paquete
// internal/collector, no del modelo. Si un valor no finito llega aquí,
// json.Marshal fallará con UnsupportedValueError. Esto es a propósito: el
// modelo es contrato estricto, el collector es defensa en profundidad.
package model

import "time"

// MetricsSnapshot es la captura puntual de todos los subsistemas monitoreados.
type MetricsSnapshot struct {
	Timestamp    time.Time      `json:"timestamp"`
	CPU          CPU            `json:"cpu"`
	Memory       Memory         `json:"memory"`
	Disks        []Disk         `json:"disks"`
	Networks     []NetworkIface `json:"networks"`
	GPU          *GPU           `json:"gpu,omitempty"`
	Battery      *Battery       `json:"battery,omitempty"`
	Docker       *Docker        `json:"docker,omitempty"`
	System       System         `json:"system"`
	TopProcesses []Process      `json:"top_processes,omitempty"`
}

// CPU agrega el uso, temperatura y frecuencia del procesador completo.
type CPU struct {
	OverallPercent float64   `json:"overall_percent"`
	PerCore        []CoreCPU `json:"per_core"`
	TempCelsius    float64   `json:"temp_celsius"`
	FreqMHzAvg     float64   `json:"freq_mhz_avg"`
}

// CoreCPU contiene las métricas de un único núcleo lógico.
type CoreCPU struct {
	ID          int     `json:"id"`
	Percent     float64 `json:"percent"`
	TempCelsius float64 `json:"temp_celsius"`
	FreqMHz     float64 `json:"freq_mhz"`
}

// Memory contiene los contadores absolutos de RAM y swap en bytes.
type Memory struct {
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	CachedBytes    uint64 `json:"cached_bytes"`
	BuffersBytes   uint64 `json:"buffers_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	SwapUsedBytes  uint64 `json:"swap_used_bytes"`
}

// Disk describe la ocupación, temperatura y throughput de un punto de montaje.
type Disk struct {
	Mountpoint         string  `json:"mountpoint"`
	Device             string  `json:"device"`
	TotalBytes         uint64  `json:"total_bytes"`
	UsedBytes          uint64  `json:"used_bytes"`
	AvailableBytes     uint64  `json:"available_bytes"`
	UsedPercent        float64 `json:"used_percent"`
	TempCelsius        float64 `json:"temp_celsius"`
	IOReadBytesPerSec  float64 `json:"io_read_bytes_per_sec"`
	IOWriteBytesPerSec float64 `json:"io_write_bytes_per_sec"`
}

// NetworkIface contiene contadores acumulados y tasas instantáneas de una interfaz de red.
type NetworkIface struct {
	Name          string  `json:"name"`
	RxBytes       uint64  `json:"rx_bytes"`
	TxBytes       uint64  `json:"tx_bytes"`
	RxBytesPerSec float64 `json:"rx_bytes_per_sec"`
	TxBytesPerSec float64 `json:"tx_bytes_per_sec"`
	IsUp          bool    `json:"is_up"`
}

// GPU describe la temperatura y carga de la unidad gráfica.
type GPU struct {
	Name        string  `json:"name"`
	TempCelsius float64 `json:"temp_celsius"`
	BusyPercent float64 `json:"busy_percent"`
}

// Battery describe el nivel de carga, estado y salud de la batería del laptop.
type Battery struct {
	Name                  string  `json:"name"`
	Percent               float64 `json:"percent"`
	Status                string  `json:"status"`
	CapacityHealthPercent float64 `json:"capacity_health_percent"`
	EnergyNowWh           float64 `json:"energy_now_wh"`
	EnergyFullWh          float64 `json:"energy_full_wh"`
	EnergyDesignWh        float64 `json:"energy_design_wh"`
}

// Docker contiene el conteo de contenedores en ejecución y totales.
type Docker struct {
	RunningContainers int `json:"running_containers"`
	TotalContainers   int `json:"total_containers"`
}

// Process es un proceso individual en el ranking de top consumidores
// del dashboard. RSSBytes es el resident set size en bytes. CPUPercent
// está normalizado a "100 % por core": un proceso que satura dos cores
// reporta 200 %. ElapsedSeconds es el tiempo desde que el proceso
// arrancó (computado contra /proc/uptime).
type Process struct {
	PID            int     `json:"pid"`
	Name           string  `json:"name"`
	RSSBytes       uint64  `json:"rss_bytes"`
	CPUPercent     float64 `json:"cpu_percent"`
	ElapsedSeconds uint64  `json:"elapsed_seconds"`
}

// System contiene uptime, load average y conteo de procesos del kernel.
type System struct {
	UptimeSeconds  uint64  `json:"uptime_seconds"`
	LoadAvg1m      float64 `json:"load_avg_1m"`
	LoadAvg5m      float64 `json:"load_avg_5m"`
	LoadAvg15m     float64 `json:"load_avg_15m"`
	ProcessesCount int     `json:"processes_count"`
}
