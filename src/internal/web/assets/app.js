/* server-monitor / dashboard renderer
 *
 * Connection model:
 *   1. GET /api/config once → store thresholds.
 *   2. EventSource on /api/stream.
 *   3. On stream error: visible reconnect with exponential backoff
 *      (1s → 2s → 4s … capped 30s). After 3 failures: fall back to
 *      polling /api/metrics every 2s; SSE keeps trying in the background.
 *
 * Safety: every dynamic insertion uses createElement + textContent, no
 * innerHTML, no template-string-into-markup. Hostile values (process
 * names from /proc, network iface names from /sys) cannot inject markup.
 */

(() => {
  "use strict";

  const REFRESH_FALLBACK_MS = 2000;
  const BACKOFF_INITIAL_MS = 1000;
  const BACKOFF_MAX_MS = 30000;
  const FAILURES_BEFORE_POLLING = 3;

  const state = {
    thresholds: null,
    eventSource: null,
    pollingTimer: null,
    failures: 0,
    backoffMs: BACKOFF_INITIAL_MS,
  };

  /* ------------------------- formatters ------------------------- */

  const fmt = {
    bytes(n) {
      if (n == null || isNaN(n)) return "—";
      const units = ["B", "KB", "MB", "GB", "TB"];
      let i = 0;
      while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
      const dec = n >= 100 ? 0 : (n >= 10 ? 1 : 2);
      return `${n.toFixed(dec)} ${units[i]}`;
    },
    bytesPerSec(n) { return n == null || isNaN(n) ? "—" : `${fmt.bytes(n)}/s`; },
    percent(n, decimals) {
      if (n == null || isNaN(n)) return "—";
      const d = decimals != null ? decimals : (n < 10 ? 1 : 0);
      return `${n.toFixed(d)}%`;
    },
    int(n) { return n == null || isNaN(n) ? "—" : Math.round(n).toString(); },
    temp(n) {
      if (n == null || isNaN(n) || n === 0) return "—";
      return `${n.toFixed(1)} °C`;
    },
    mhz(n) { return n == null || isNaN(n) ? "—" : `${(n / 1000).toFixed(2)} GHz`; },
    uptime(seconds) {
      if (!seconds) return "—";
      const d = Math.floor(seconds / 86400);
      const h = Math.floor((seconds % 86400) / 3600);
      const m = Math.floor((seconds % 3600) / 60);
      if (d > 0) return `${d}d ${h}h`;
      if (h > 0) return `${h}h ${m}m`;
      return `${m}m`;
    },
    etime(seconds) {
      if (!seconds || seconds === 0) return "—";
      const d = Math.floor(seconds / 86400);
      const h = Math.floor((seconds % 86400) / 3600);
      const m = Math.floor((seconds % 3600) / 60);
      const s = Math.floor(seconds % 60);
      if (d > 0) return `${d}d${h}h`;
      if (h > 0) return `${h}h${m}m`;
      if (m > 0) return `${m}m${String(s).padStart(2, "0")}s`;
      return `${s}s`;
    },
    load(n) { return n == null ? "—" : n.toFixed(2); },
    timestamp(iso) {
      if (!iso) return "—";
      return new Date(iso).toLocaleTimeString("es-CL", { hour12: false });
    },
    date(iso) {
      if (!iso) return "—";
      const d = new Date(iso);
      return d.toLocaleString("es-CL", {
        weekday: "short", day: "2-digit", month: "short",
        hour: "2-digit", minute: "2-digit", hour12: false,
      });
    },
  };

  /* ---------------------- threshold colouring ----------------------- */

  function stateForHigh(value, warn, crit) {
    if (value == null || warn == null) return "ok";
    if (value >= crit) return "crit";
    if (value >= warn) return "warn";
    return "ok";
  }

  function stateForLow(value, warn, crit) {
    if (value == null || warn == null) return "ok";
    if (value <= crit) return "crit";
    if (value <= warn) return "warn";
    return "ok";
  }

  function colorFor(stateName) {
    switch (stateName) {
      case "crit": return "var(--crit)";
      case "warn": return "var(--warn)";
      default:     return "var(--paper)";
    }
  }

  function thresh(key) {
    return state.thresholds ? state.thresholds[key] : undefined;
  }

  /* -------------------------- DOM helpers --------------------------- */

  function el(tag, opts = {}, children = []) {
    const node = document.createElement(tag);
    if (opts.class) node.className = opts.class;
    if (opts.text != null) node.textContent = opts.text;
    if (opts.style) Object.assign(node.style, opts.style);
    if (opts.attrs) {
      for (const [k, v] of Object.entries(opts.attrs)) node.setAttribute(k, v);
    }
    for (const child of children) {
      if (child) node.appendChild(child);
    }
    return node;
  }

  function setText(id, value) {
    const node = document.getElementById(id);
    if (node) node.textContent = value;
  }

  function setBar(id, percent, color) {
    const node = document.getElementById(id);
    if (!node) return;
    node.style.width = `${Math.max(0, Math.min(100, percent || 0))}%`;
    if (color) node.style.background = color;
  }

  function setHeroState(kpiName, stateName) {
    const node = document.querySelector(`.kpi[data-kpi="${kpiName}"]`);
    if (node) node.dataset.state = stateName;
  }

  /* ---------------------- per-section renderers --------------------- */

  function renderHero(snap) {
    const cpu = snap.cpu || {};
    const mem = snap.memory || {};
    const sys = snap.system || {};
    const disks = snap.disks || [];

    // CPU
    const cpuPct = cpu.overall_percent ?? 0;
    const cpuState = stateForHigh(cpuPct, thresh("cpu_usage_warn"), thresh("cpu_usage_crit"));
    setText("hero-cpu", fmt.int(cpuPct));
    setText("hero-cpu-sub", `${fmt.temp(cpu.temp_celsius)} · ${fmt.mhz(cpu.freq_mhz_avg)}`);
    setHeroState("cpu", cpuState);

    // Memory
    const total = mem.total_bytes || 0;
    const used = mem.used_bytes || 0;
    const memPct = total > 0 ? (used / total) * 100 : 0;
    const memState = stateForHigh(memPct, thresh("mem_usage_warn"), thresh("mem_usage_crit"));
    setText("hero-mem", fmt.int(memPct));
    setText("hero-mem-sub", `${fmt.bytes(used)} de ${fmt.bytes(total)}`);
    setHeroState("mem", memState);

    // Disk (highest used%)
    let topDisk = null;
    for (const d of disks) {
      if (!topDisk || (d.used_percent || 0) > (topDisk.used_percent || 0)) topDisk = d;
    }
    if (topDisk) {
      const diskState = stateForHigh(topDisk.used_percent || 0,
        thresh("disk_usage_warn"), thresh("disk_usage_crit"));
      setText("hero-disk", fmt.int(topDisk.used_percent));
      setText("hero-disk-sub", `${topDisk.mountpoint} · ${topDisk.device}`);
      setHeroState("disk", diskState);
    }

    // Temp (CPU package)
    const tempVal = cpu.temp_celsius ?? 0;
    const tempState = (tempVal > 0) ?
      stateForHigh(tempVal, thresh("cpu_temp_warn"), thresh("cpu_temp_crit")) : "ok";
    setText("hero-temp", tempVal > 0 ? Math.round(tempVal).toString() : "—");
    setText("hero-temp-sub", "CPU package · k10temp");
    setHeroState("temp", tempState);

    // Uptime
    setText("hero-uptime", fmt.uptime(sys.uptime_seconds));
    setText("hero-uptime-sub", `load 1m: ${fmt.load(sys.load_avg_1m)} · ${sys.processes_count ?? "—"} procesos`);
  }

  function renderCPUPanel(cpu) {
    const cores = cpu.per_core || [];
    setText("cpu-panel-meta",
      cores.length > 0
        ? `${cores.length} hilos · ${fmt.mhz(cpu.freq_mhz_avg)}`
        : "—");

    const container = document.getElementById("cpu-cores");
    if (container.children.length !== cores.length) {
      container.replaceChildren(...cores.map(c =>
        el("div", { class: "core", attrs: { "data-core": String(c.id) } }, [
          el("span", { class: "core-id", text: `c${String(c.id).padStart(2, "0")}` }),
          el("div", { class: "core-pct" }, [
            el("span", { class: "core-pct-val", text: "—" }),
            el("em", { text: "%" }),
          ]),
          el("div", { class: "core-bar" }, [el("div", { class: "core-bar-fill" })]),
        ])
      ));
    }
    cores.forEach(core => {
      const node = container.querySelector(`[data-core="${core.id}"]`);
      if (!node) return;
      const pct = core.percent ?? 0;
      const stateName = stateForHigh(pct, thresh("cpu_usage_warn"), thresh("cpu_usage_crit"));
      const valNode = node.querySelector(".core-pct-val");
      valNode.textContent = fmt.int(pct);
      valNode.style.color = colorFor(stateName);
      const fill = node.querySelector(".core-bar-fill");
      fill.style.width = `${Math.min(100, pct)}%`;
      fill.style.background = colorFor(stateName);
    });
  }

  function renderMemoryPanel(mem) {
    const total = mem.total_bytes || 0;
    const used = mem.used_bytes || 0;
    const pct = total > 0 ? (used / total) * 100 : 0;
    const stateName = stateForHigh(pct, thresh("mem_usage_warn"), thresh("mem_usage_crit"));
    setBar("mem-bar-fill", pct, colorFor(stateName));
    setText("mem-pct", fmt.percent(pct, 1));
    setText("mem-byte", `${fmt.bytes(used)} de ${fmt.bytes(total)}`);
    setText("mem-avail", fmt.bytes(mem.available_bytes));
    setText("mem-cache", fmt.bytes(mem.cached_bytes));
    setText("mem-buffers", fmt.bytes(mem.buffers_bytes));
    const swapTotal = mem.swap_total_bytes || 0;
    setText("mem-swap", swapTotal === 0 ? "—" :
      `${fmt.bytes(mem.swap_used_bytes || 0)} / ${fmt.bytes(swapTotal)}`);
    setText("mem-panel-meta", `${fmt.bytes(total)} totales`);
  }

  function renderDisks(disks) {
    const body = document.getElementById("disks-body");
    if (!disks || disks.length === 0) {
      body.replaceChildren(el("p", { class: "placeholder", text: "sin datos" }));
      return;
    }
    body.replaceChildren(...disks.map(d => {
      const pct = d.used_percent ?? 0;
      const stateName = stateForHigh(pct, thresh("disk_usage_warn"), thresh("disk_usage_crit"));
      const tempState = (d.temp_celsius && d.temp_celsius > 0)
        ? stateForHigh(d.temp_celsius, thresh("disk_temp_warn"), thresh("disk_temp_crit"))
        : "ok";
      const tempStr = fmt.temp(d.temp_celsius);
      const tempEl = el("span", {
        text: tempStr,
        style: (d.temp_celsius && d.temp_celsius > 0) ? { color: colorFor(tempState) } : {},
      });

      const labelChildren = [
        el("span", { class: "row-name", text: d.mountpoint }),
        el("span", { class: "row-sub" }, [
          document.createTextNode(`${d.device} · `),
          tempEl,
        ]),
      ];
      const barFill = el("div", {
        class: "row-bar-fill",
        style: { width: `${Math.min(100, pct)}%`, background: colorFor(stateName) },
      });

      return el("div", { class: "row" }, [
        el("div", { class: "row-label" }, labelChildren),
        el("div", { class: "row-bar" }, [barFill]),
        el("div", { class: "row-numbers" }, [
          el("span", {
            class: "row-num-primary",
            text: `${fmt.bytes(d.available_bytes)} libres`,
          }),
          el("span", {
            class: "row-num-sub",
            text: `${fmt.percent(pct, 0)} usado · total ${fmt.bytes(d.total_bytes)}`,
            style: { color: colorFor(stateName) },
          }),
          el("span", {
            class: "row-num-sub",
            text: `↓${fmt.bytesPerSec(d.io_read_bytes_per_sec)} ↑${fmt.bytesPerSec(d.io_write_bytes_per_sec)}`,
          }),
        ]),
      ]);
    }));
  }

  function renderNetwork(networks) {
    const body = document.getElementById("net-body");
    if (!networks || networks.length === 0) {
      body.replaceChildren(el("p", { class: "placeholder", text: "sin interfaces" }));
      return;
    }
    body.replaceChildren(...networks.map(n => {
      const stateBadge = el("span", {
        class: `iface-state ${n.is_up ? "up" : "down"}`,
        text: n.is_up ? "up" : "down",
      });
      const nameSpan = el("span", { class: "row-name" }, [
        document.createTextNode(n.name),
        stateBadge,
      ]);
      return el("div", { class: "row" }, [
        el("div", { class: "row-label" }, [
          nameSpan,
          el("span", {
            class: "row-sub",
            text: `total ↓${fmt.bytes(n.rx_bytes)} · ↑${fmt.bytes(n.tx_bytes)}`,
          }),
        ]),
        // Empty middle slot to keep grid alignment with disks panel
        el("div"),
        el("div", { class: "row-numbers" }, [
          el("span", {
            class: "row-num-primary",
            text: `↓${fmt.bytesPerSec(n.rx_bytes_per_sec)}`,
          }),
          el("span", {
            class: "row-num-sub",
            text: `↑${fmt.bytesPerSec(n.tx_bytes_per_sec)}`,
          }),
        ]),
      ]);
    }));
  }

  function renderGPU(gpu) {
    const meta = document.getElementById("gpu-meta");
    if (!gpu) {
      meta.textContent = "no detectada";
      setText("gpu-busy", "—");
      setText("gpu-temp", "—");
      setBar("gpu-bar", 0, "var(--paper-ghost)");
      return;
    }
    meta.textContent = gpu.name || "GPU";
    const busy = gpu.busy_percent ?? 0;
    const busyState = stateForHigh(busy, 60, 85);
    setText("gpu-busy", fmt.int(busy));
    setBar("gpu-bar", busy, colorFor(busyState));
    const t = gpu.temp_celsius ?? 0;
    const tempEl = document.getElementById("gpu-temp");
    tempEl.textContent = t > 0 ? fmt.int(t) : "—";
    if (t > 0) {
      const tempState = stateForHigh(t, thresh("gpu_temp_warn"), thresh("gpu_temp_crit"));
      tempEl.style.color = colorFor(tempState);
    } else {
      tempEl.style.color = "";
    }
  }

  function renderBattery(bat) {
    const status = document.getElementById("battery-status");
    if (!bat) {
      status.textContent = "no detectada";
      setText("battery-pct", "—");
      setText("battery-health", "—");
      setText("battery-energy", "—");
      setBar("battery-bar", 0, "var(--paper-ghost)");
      return;
    }
    status.textContent = (bat.status || "—").toLowerCase();
    const pct = bat.percent ?? 0;
    const stateName = stateForLow(pct, thresh("battery_warn"), thresh("battery_crit"));
    setText("battery-pct", fmt.int(pct));
    setBar("battery-bar", pct, colorFor(stateName));
    setText("battery-health", bat.capacity_health_percent != null
      ? fmt.percent(bat.capacity_health_percent, 0)
      : "—");
    setText("battery-energy",
      (bat.energy_now_wh && bat.energy_full_wh)
        ? `${bat.energy_now_wh.toFixed(1)} / ${bat.energy_full_wh.toFixed(1)} Wh`
        : "—");
  }

  function renderDocker(docker) {
    setText("docker-running", docker?.running_containers ?? "—");
    setText("docker-total", docker?.total_containers ?? "—");
  }

  function renderSystem(sys) {
    setText("sys-uptime", fmt.uptime(sys.uptime_seconds));
    setText("sys-load1", fmt.load(sys.load_avg_1m));
    setText("sys-load5", fmt.load(sys.load_avg_5m));
    setText("sys-load15", fmt.load(sys.load_avg_15m));
    setText("sys-procs", sys.processes_count ?? "—");
  }

  function renderProcesses(procs) {
    const body = document.getElementById("procs-body");
    if (!procs || procs.length === 0) {
      body.replaceChildren(
        el("tr", {}, [
          el("td", {
            class: "placeholder",
            text: "sin datos",
            attrs: { colspan: "7" },
          }),
        ])
      );
      return;
    }
    body.replaceChildren(...procs.map((p, i) => {
      const killBtn = el("button", { class: "proc-kill-btn", attrs: { title: `Terminar ${p.name}` }, text: "×" });
      killBtn.addEventListener("click", () => showKillModal(p));
      return el("tr", {}, [
        el("td", { class: "procs-rank", text: String(i + 1).padStart(2, "0") }),
        el("td", { text: p.name }),
        el("td", { class: "procs-num", text: String(p.pid) }),
        el("td", { class: "procs-num", text: fmt.bytes(p.rss_bytes) }),
        el("td", { class: "procs-num", text: fmt.percent(p.cpu_percent, 2) }),
        el("td", { class: "procs-num", text: fmt.etime(p.elapsed_seconds) }),
        el("td", { class: "procs-num" }, [killBtn]),
      ]);
    }));
  }

  /* ----------------------- kill modal ----------------------------- */

  function showKillModal(proc) {
    const overlay = el("div", { class: "kill-overlay" });
    const modal = el("div", { class: "kill-modal" }, [
      el("h3", { class: "kill-title", text: "Terminar proceso" }),
      el("p", { class: "kill-info" }, [
        document.createTextNode("¿Enviar "),
        el("code", { text: "SIGTERM" }),
        document.createTextNode(" a:"),
      ]),
      el("div", { class: "kill-proc" }, [
        el("span", { class: "kill-proc-name", text: proc.name }),
        el("span", { class: "kill-proc-meta", text: `PID ${proc.pid} · ${fmt.bytes(proc.rss_bytes)} RAM` }),
      ]),
      el("div", { class: "kill-actions" }, [
        (() => {
          const cancel = el("button", { class: "kill-btn kill-btn-cancel", text: "Cancelar" });
          cancel.addEventListener("click", () => document.body.removeChild(overlay));
          return cancel;
        })(),
        (() => {
          const confirm = el("button", { class: "kill-btn kill-btn-confirm", text: "Terminar" });
          confirm.addEventListener("click", () => {
            confirm.disabled = true;
            confirm.textContent = "…";
            fetch(`/api/processes/${proc.pid}/kill`, { method: "POST" })
              .then(r => r.json())
              .then(data => {
                confirm.textContent = data.error ? "Error" : "Enviado";
                setTimeout(() => document.body.removeChild(overlay), 1000);
              })
              .catch(() => {
                confirm.textContent = "Error";
                setTimeout(() => document.body.removeChild(overlay), 1500);
              });
          });
          return confirm;
        })(),
      ]),
    ]);
    overlay.appendChild(modal);
    overlay.addEventListener("click", e => { if (e.target === overlay) document.body.removeChild(overlay); });
    document.body.appendChild(overlay);
  }

  function render(snapshot) {
    if (!snapshot) return;
    renderHero(snapshot);
    renderCPUPanel(snapshot.cpu || {});
    renderMemoryPanel(snapshot.memory || {});
    renderDisks(snapshot.disks);
    renderNetwork(snapshot.networks);
    renderGPU(snapshot.gpu);
    renderBattery(snapshot.battery);
    renderDocker(snapshot.docker);
    renderSystem(snapshot.system || {});
    renderProcesses(snapshot.top_processes);
    setText("masthead-time", fmt.date(snapshot.timestamp));
    setText("footer-time", `actualizado ${fmt.timestamp(snapshot.timestamp)}`);
  }

  /* ----------------------- connection state ------------------------ */

  function setConnectionState(label, stateName) {
    const node = document.getElementById("connection-status");
    node.dataset.state = stateName;
    node.querySelector(".status-label").textContent = label;
  }

  /* ----------------------------- SSE ------------------------------- */

  function connectSSE() {
    if (state.eventSource) {
      state.eventSource.close();
      state.eventSource = null;
    }
    setConnectionState("conectando", "connecting");
    const es = new EventSource("/api/stream");
    state.eventSource = es;

    es.onopen = () => {
      state.failures = 0;
      state.backoffMs = BACKOFF_INITIAL_MS;
      stopPolling();
      setConnectionState("en línea", "connected");
    };

    es.onmessage = (e) => {
      try {
        render(JSON.parse(e.data));
      } catch (err) {
        console.warn("SSE parse error:", err);
      }
    };

    es.onerror = () => {
      es.close();
      state.eventSource = null;
      state.failures += 1;
      const delay = Math.min(state.backoffMs, BACKOFF_MAX_MS);
      state.backoffMs = Math.min(state.backoffMs * 2, BACKOFF_MAX_MS);

      if (state.failures >= FAILURES_BEFORE_POLLING) {
        setConnectionState("polling 2s", "polling");
        startPolling();
      } else {
        setConnectionState(`reconexión ${Math.round(delay / 1000)}s`, "reconnecting");
      }
      setTimeout(connectSSE, delay);
    };
  }

  function startPolling() {
    if (state.pollingTimer) return;
    pollOnce();
    state.pollingTimer = setInterval(pollOnce, REFRESH_FALLBACK_MS);
  }

  function stopPolling() {
    if (state.pollingTimer) {
      clearInterval(state.pollingTimer);
      state.pollingTimer = null;
    }
  }

  function pollOnce() {
    fetch("/api/metrics")
      .then(r => r.ok ? r.json() : Promise.reject(new Error(r.statusText)))
      .then(snap => render(snap))
      .catch(err => console.warn("poll error:", err));
  }

  /* ------------------------- bootstrap ----------------------------- */

  function loadConfig() {
    return fetch("/api/config")
      .then(r => r.ok ? r.json() : null)
      .then(t => { state.thresholds = t; })
      .catch(err => console.warn("config error:", err));
  }

  loadConfig().then(connectSSE);
})();
