package collector

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/jordisama/server-monitor/internal/model"
)

// SnapshotterConfig wires the dependencies of a Snapshotter. All
// collectors are required (the model exposes them all). TTLs are the
// caché diferenciada del plan: smartctl es caro y cambia poco, sensors
// es relativamente caro pero puede cambiar segundo a segundo, el resto
// se lee fresco en cada Snapshot.
//
// SmartctlExec / SensorsExec son hooks de testing. Si quedan en nil, se
// usa el exec real (smartctl + SensorsCollector.Collect respectivamente).
type SnapshotterConfig struct {
	CPU       *CPUCollector
	Memory    *MemoryCollector
	Disk      *DiskCollector
	Network   *NetworkCollector
	Battery   *BatteryCollector
	Docker    *DockerCollector
	Sensors   *SensorsCollector
	Processes *ProcessesCollector
	System    *SystemCollector

	GPUName string

	SmartTTL   time.Duration
	SensorsTTL time.Duration

	SmartctlExec func(device string) float64
	SensorsExec  func() (SensorsResult, error)
}

type cachedSmartTemp struct {
	temp float64
	asof time.Time
}

type cachedSensors struct {
	value SensorsResult
	err   error
	asof  time.Time
	valid bool
}

// Snapshotter compone los 9 sub-collectors en un MetricsSnapshot. Es el
// único lugar del código que conoce todos los collectors juntos; el API
// HTTP (Fase 3.4) le pide snapshots y los serializa.
//
// Caché diferenciada:
//   - smartctl: TTL configurable (típico 60 s) por device.
//   - sensors: TTL configurable (típico 1 s) compartido para toda la
//     llamada a `sensors -j`.
//   - resto de collectors: se ejecutan fresh cada Snapshot.
//
// Snapshot es safe-for-concurrent-use vía mutex. Snapshot siempre
// devuelve un MetricsSnapshot con Timestamp; los errores de sub-
// collectors se acumulan vía errors.Join. Sub-collectors opcionales
// (Battery, Docker, GPU) que devuelven nil/no-data dejan los pointers
// del snapshot en nil sin generar error.
type Snapshotter struct {
	cfg SnapshotterConfig

	mu          sync.Mutex
	sensorsLast cachedSensors
	smartLast   map[string]cachedSmartTemp

	now       func() time.Time
	onSnapshot []func(model.MetricsSnapshot)
}

// OnSnapshot registers a callback invoked after every successful Snapshot call.
// Safe to call before the first Snapshot.
func (s *Snapshotter) OnSnapshot(fn func(model.MetricsSnapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSnapshot = append(s.onSnapshot, fn)
}

// NewSnapshotter wires a Snapshotter from its config. It also points
// cfg.Disk's temperature provider at the cached smartctl wrapper so
// that DiskCollector.Collect uses the cached value transparently.
func NewSnapshotter(cfg SnapshotterConfig) *Snapshotter {
	s := &Snapshotter{
		cfg:       cfg,
		smartLast: make(map[string]cachedSmartTemp),
		now:       time.Now,
	}
	if s.cfg.SmartctlExec == nil {
		s.cfg.SmartctlExec = defaultSmartctlExec
	}
	if s.cfg.SensorsExec == nil {
		s.cfg.SensorsExec = func() (SensorsResult, error) {
			return s.cfg.Sensors.Collect()
		}
	}
	if cfg.Disk != nil {
		cfg.Disk.SetTempProvider(s.cachedSmartTemp)
	}
	return s
}

func defaultSmartctlExec(device string) float64 {
	out, err := exec.Command("smartctl", "-j", "-A", device).Output()
	if err != nil && len(out) == 0 {
		return 0
	}
	temp, err := parseSmartctlTemp(out)
	if err != nil {
		return 0
	}
	return temp
}

// cachedSmartTemp returns the cached smartctl temperature for device,
// re-running the underlying call only when the entry is older than
// SmartTTL. Safe for concurrent use.
func (s *Snapshotter) cachedSmartTemp(device string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if c, ok := s.smartLast[device]; ok && now.Sub(c.asof) < s.cfg.SmartTTL {
		return c.temp
	}
	temp := s.cfg.SmartctlExec(device)
	s.smartLast[device] = cachedSmartTemp{temp: temp, asof: now}
	return temp
}

func (s *Snapshotter) cachedSensors() (SensorsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.sensorsLast.valid && now.Sub(s.sensorsLast.asof) < s.cfg.SensorsTTL {
		return s.sensorsLast.value, s.sensorsLast.err
	}
	value, err := s.cfg.SensorsExec()
	s.sensorsLast = cachedSensors{value: value, err: err, asof: now, valid: true}
	return value, err
}

// Snapshot returns the current MetricsSnapshot. The Timestamp is always
// populated; sub-collector errors are joined into the returned error
// but do NOT prevent the snapshot from being returned with whatever
// data was available.
func (s *Snapshotter) Snapshot() (model.MetricsSnapshot, error) {
	snap := model.MetricsSnapshot{Timestamp: s.now().UTC()}
	var errs []error

	cpu, err := s.cfg.CPU.Collect()
	if err != nil {
		errs = append(errs, fmt.Errorf("cpu: %w", err))
	} else {
		snap.CPU = cpu
	}

	if mem, err := s.cfg.Memory.Collect(); err != nil {
		errs = append(errs, fmt.Errorf("memory: %w", err))
	} else {
		snap.Memory = mem
	}

	if disks, err := s.cfg.Disk.Collect(); err != nil {
		errs = append(errs, fmt.Errorf("disk: %w", err))
	} else {
		snap.Disks = disks
	}

	if nets, err := s.cfg.Network.Collect(); err != nil {
		errs = append(errs, fmt.Errorf("network: %w", err))
	} else {
		snap.Networks = nets
	}

	if bat, err := s.cfg.Battery.Collect(); err != nil {
		errs = append(errs, fmt.Errorf("battery: %w", err))
	} else {
		snap.Battery = bat
	}

	if docker, err := s.cfg.Docker.Collect(); err != nil {
		errs = append(errs, fmt.Errorf("docker: %w", err))
	} else {
		snap.Docker = docker
	}

	if procs, err := s.cfg.Processes.Collect(); err != nil {
		errs = append(errs, fmt.Errorf("processes: %w", err))
	} else {
		snap.TopProcesses = procs
	}

	if sys, err := s.cfg.System.Collect(); err != nil {
		errs = append(errs, fmt.Errorf("system: %w", err))
	} else {
		snap.System = sys
	}

	sens, sensErr := s.cachedSensors()
	if sensErr != nil {
		errs = append(errs, fmt.Errorf("sensors: %w", sensErr))
	}
	snap.CPU = applySensorsToCPU(snap.CPU, sens)
	if sens.GPUTempCelsius > 0 || sens.GPUBusyPercent > 0 {
		snap.GPU = &model.GPU{
			Name:        s.cfg.GPUName,
			TempCelsius: sens.GPUTempCelsius,
			BusyPercent: sens.GPUBusyPercent,
		}
	}

	joinedErr := errors.Join(errs...)
	if joinedErr == nil {
		s.mu.Lock()
		cbs := s.onSnapshot
		s.mu.Unlock()
		for _, fn := range cbs {
			fn(snap)
		}
	}
	return snap, joinedErr
}
