package collector

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fakeProc represents one synthetic /proc/<pid>/ entry.
type fakeProc struct {
	status string
	stat   string
}

// writeFakeProc materializes a /proc-like tree: a top-level "stat" file
// (whatever the caller passes for the cpu/cpuN aggregate) plus a
// status+stat pair under each numeric pid directory.
func writeFakeProc(t *testing.T, dir string, statTop string, procs map[int]fakeProc) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(statTop), 0644); err != nil {
		t.Fatal(err)
	}
	for pid, p := range procs {
		pidDir := filepath.Join(dir, strconv.Itoa(pid))
		if err := os.MkdirAll(pidDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pidDir, "status"), []byte(p.status), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pidDir, "stat"), []byte(p.stat), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func mkStatus(name string, vmRSSKiB uint64) string {
	return fmt.Sprintf("Name:\t%s\nState:\tS (sleeping)\nVmRSS:\t%d kB\n", name, vmRSSKiB)
}

func mkStat(pid int, comm string, utime, stime uint64) string {
	return mkStatFull(pid, comm, utime, stime, 1000)
}

func mkStatFull(pid int, comm string, utime, stime, starttime uint64) string {
	// After paren: state(0), ppid(1), pgrp(2), session(3), tty_nr(4),
	// tpgid(5), flags(6), minflt(7), cminflt(8), majflt(9), cmajflt(10),
	// utime(11), stime(12), cutime(13), cstime(14), priority(15),
	// nice(16), num_threads(17), itrealvalue(18), starttime(19).
	return fmt.Sprintf("%d (%s) S 1 1 1 0 -1 0 0 0 0 0 %d %d 0 0 20 0 1 0 %d 0 0 0 0 0\n",
		pid, comm, utime, stime, starttime)
}

func mkAggregateStat(jiffiesPerCore, numCores uint64) string {
	// "cpu" line + N "cpuN" lines so parseCPUStat reports numCores cores.
	out := fmt.Sprintf("cpu  %d 0 0 0 0 0 0 0 0 0\n", jiffiesPerCore*numCores)
	for i := uint64(0); i < numCores; i++ {
		out += fmt.Sprintf("cpu%d %d 0 0 0 0 0 0 0 0 0\n", i, jiffiesPerCore)
	}
	out += "intr 0\nctxt 0\n"
	return out
}

func TestParseStatusExtractsNameAndRSS(t *testing.T) {
	t.Parallel()
	name, rss, ok := parseStatus([]byte(mkStatus("postgres", 12648)))
	if !ok {
		t.Fatal("parseStatus returned ok=false")
	}
	if name != "postgres" {
		t.Errorf("name = %q, want postgres", name)
	}
	if rss != 12648*1024 {
		t.Errorf("rss = %d, want %d", rss, 12648*1024)
	}
}

func TestParseStatusMissingFieldsReturnsNotOK(t *testing.T) {
	t.Parallel()
	if _, _, ok := parseStatus([]byte("State:\tR (running)\n")); ok {
		t.Error("parseStatus should be ok=false when Name missing")
	}
}

func TestParseStatusKernelThreadHasNoRSS(t *testing.T) {
	t.Parallel()
	// Kernel threads (kworker, kthreadd) have Name but no VmRSS line.
	name, rss, ok := parseStatus([]byte("Name:\tkworker/0:1\nState:\tI (idle)\n"))
	if !ok || name != "kworker/0:1" {
		t.Errorf("got name=%q ok=%v, want kworker ok=true", name, ok)
	}
	if rss != 0 {
		t.Errorf("kernel thread rss = %d, want 0", rss)
	}
}

func TestParseStatExtractsJiffiesAndStarttime(t *testing.T) {
	t.Parallel()
	st, err := parseStat([]byte(mkStatFull(1234, "node", 100, 50, 5000)))
	if err != nil {
		t.Fatalf("parseStat: %v", err)
	}
	if st.cpuJiffies != 150 {
		t.Errorf("cpuJiffies = %d, want 150 (utime 100 + stime 50)", st.cpuJiffies)
	}
	if st.startTicks != 5000 {
		t.Errorf("startTicks = %d, want 5000", st.startTicks)
	}
}

func TestParseStatHandlesCommWithSpacesAndParens(t *testing.T) {
	t.Parallel()
	raw := "1234 (my (ugly) name) S 1 1 1 0 -1 0 0 0 0 0 200 30 0 0 20 0 1 0 7777 0 0 0 0 0\n"
	st, err := parseStat([]byte(raw))
	if err != nil {
		t.Fatalf("parseStat: %v", err)
	}
	if st.cpuJiffies != 230 {
		t.Errorf("cpuJiffies = %d, want 230 (200+30)", st.cpuJiffies)
	}
	if st.startTicks != 7777 {
		t.Errorf("startTicks = %d, want 7777", st.startTicks)
	}
}

func TestParseStatRejectsMalformed(t *testing.T) {
	t.Parallel()
	if _, err := parseStat([]byte("garbage no paren here")); err == nil {
		t.Error("expected error for stat without closing paren")
	}
	if _, err := parseStat([]byte("1 (a) S 1 2 3\n")); err == nil {
		t.Error("expected error for stat with too few fields")
	}
}

func TestProcessesCollectorComputesElapsedSeconds(t *testing.T) {
	dir := t.TempDir()
	// uptime 1000s, proc starttime = 200 ticks (= 2s with USER_HZ 100).
	// elapsed_seconds = 1000 - 2 = 998.
	if err := os.WriteFile(filepath.Join(dir, "uptime"), []byte("1000.00 9999.00\n"), 0644); err != nil {
		t.Fatal(err)
	}
	procs := map[int]fakeProc{
		111: {
			status: mkStatus("oldproc", 1000),
			stat:   mkStatFull(111, "oldproc", 0, 0, 200),
		},
	}
	writeFakeProc(t, dir, mkAggregateStat(1000, 4), procs)
	c := NewProcessesCollector(dir, 5)
	out, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].ElapsedSeconds != 998 {
		t.Errorf("ElapsedSeconds = %d, want 998", out[0].ElapsedSeconds)
	}
}

func TestProcessesCollectorTopNByRSS(t *testing.T) {
	dir := t.TempDir()
	procs := map[int]fakeProc{
		100: {status: mkStatus("low", 100), stat: mkStat(100, "low", 1, 0)},
		200: {status: mkStatus("medium", 5000), stat: mkStat(200, "medium", 10, 0)},
		300: {status: mkStatus("high", 50000), stat: mkStat(300, "high", 100, 0)},
		400: {status: mkStatus("kthread", 0), stat: mkStat(400, "kthread", 0, 0)},
	}
	writeFakeProc(t, dir, mkAggregateStat(1000, 4), procs)
	c := NewProcessesCollector(dir, 2)
	out, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d processes, want top-2", len(out))
	}
	if out[0].Name != "high" || out[1].Name != "medium" {
		t.Errorf("ranking wrong: %+v", out)
	}
	if out[0].RSSBytes != 50000*1024 {
		t.Errorf("top RSS = %d, want %d", out[0].RSSBytes, 50000*1024)
	}
	for _, p := range out {
		if p.CPUPercent != 0 {
			t.Errorf("first-call CPU%% must be 0, got %f for pid %d", p.CPUPercent, p.PID)
		}
	}
}

func TestProcessesCollectorComputesCPUDeltaAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	// First call: pid 999 has utime=0, total CPU jiffies = 4 cores × 1000.
	procs1 := map[int]fakeProc{
		999: {status: mkStatus("burner", 100000), stat: mkStat(999, "burner", 0, 0)},
	}
	writeFakeProc(t, dir, mkAggregateStat(1000, 4), procs1)
	c := NewProcessesCollector(dir, 5)
	if _, err := c.Collect(); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	// Second call: pid 999 ate 400 jiffies, total grew by 4000 (system idle apart from this proc).
	procs2 := map[int]fakeProc{
		999: {status: mkStatus("burner", 100000), stat: mkStat(999, "burner", 400, 0)},
	}
	writeFakeProc(t, dir, mkAggregateStat(2000, 4), procs2)
	out, err := c.Collect()
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	// Δproc=400, Δtotal=4000 → 400/4000 × 100 = 10% of total system CPU.
	if out[0].CPUPercent < 9 || out[0].CPUPercent > 11 {
		t.Errorf("CPUPercent = %f, want ≈ 10", out[0].CPUPercent)
	}
}

func TestProcessesCollectorSkipsVanishedProcesses(t *testing.T) {
	dir := t.TempDir()
	writeFakeProc(t, dir, mkAggregateStat(1000, 4), map[int]fakeProc{
		111: {status: mkStatus("alive", 1000), stat: mkStat(111, "alive", 1, 0)},
	})
	// Create a pid dir with no readable files — simulates a process that
	// vanished between listing and reading.
	dead := filepath.Join(dir, "222")
	if err := os.Mkdir(dead, 0755); err != nil {
		t.Fatal(err)
	}
	c := NewProcessesCollector(dir, 5)
	out, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(out) != 1 || out[0].PID != 111 {
		t.Errorf("expected only pid 111, got %+v", out)
	}
}

func TestProcessesCollectorIgnoresNonNumericDirs(t *testing.T) {
	dir := t.TempDir()
	writeFakeProc(t, dir, mkAggregateStat(1000, 4), map[int]fakeProc{
		111: {status: mkStatus("alive", 1000), stat: mkStat(111, "alive", 1, 0)},
	})
	// /proc has lots of non-numeric entries: self, sys, net, etc.
	for _, name := range []string{"self", "sys", "net", "kmsg-doesnt-exist"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	c := NewProcessesCollector(dir, 5)
	out, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("non-numeric dirs leaked: got %+v", out)
	}
}

func TestProcessesCollectorPreservesLastForProcessOutOfTopN(t *testing.T) {
	dir := t.TempDir()
	// First call: 3 processes, top-1 = pid 300. We collect top-1 only.
	procs1 := map[int]fakeProc{
		100: {status: mkStatus("a", 100), stat: mkStat(100, "a", 0, 0)},
		200: {status: mkStatus("b", 200), stat: mkStat(200, "b", 0, 0)},
		300: {status: mkStatus("c", 5000), stat: mkStat(300, "c", 0, 0)},
	}
	writeFakeProc(t, dir, mkAggregateStat(1000, 4), procs1)
	c := NewProcessesCollector(dir, 1)
	if _, err := c.Collect(); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	// Second call: pid 100 grew its RSS over 300 AND consumed 400 jiffies
	// while pid 300 stayed idle. pid 100 should now be top-1 with a real
	// CPU% (not 0) — proving lastJiffies were stored even though pid 100
	// wasn't in the previous output.
	procs2 := map[int]fakeProc{
		100: {status: mkStatus("a", 99999), stat: mkStat(100, "a", 400, 0)},
		200: {status: mkStatus("b", 200), stat: mkStat(200, "b", 0, 0)},
		300: {status: mkStatus("c", 5000), stat: mkStat(300, "c", 0, 0)},
	}
	writeFakeProc(t, dir, mkAggregateStat(2000, 4), procs2)
	out, err := c.Collect()
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if len(out) != 1 || out[0].PID != 100 {
		t.Fatalf("expected top-1 pid=100, got %+v", out)
	}
	if out[0].CPUPercent <= 0 {
		t.Errorf("pid 100 CPU%% = %f, want > 0 (last must have been preserved)", out[0].CPUPercent)
	}
}

func TestProcessesCollectorErrorsOnMissingProc(t *testing.T) {
	t.Parallel()
	c := NewProcessesCollector(t.TempDir(), 5)
	if _, err := c.Collect(); err == nil {
		t.Error("expected error: tempdir has no /stat file")
	}
}

func TestProcessesCollectorAgainstLiveProc(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no live /proc")
	}
	c := NewProcessesCollector("/proc", 10)
	out, err := c.Collect()
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("live system should have at least one process")
	}
	// Top should be sorted descending by RSS.
	for i := 1; i < len(out); i++ {
		if out[i].RSSBytes > out[i-1].RSSBytes {
			t.Errorf("not sorted desc: out[%d].RSS=%d > out[%d].RSS=%d",
				i, out[i].RSSBytes, i-1, out[i-1].RSSBytes)
		}
	}
	for _, p := range out {
		if p.PID <= 0 {
			t.Errorf("bad pid: %+v", p)
		}
		if p.Name == "" {
			t.Errorf("empty name: %+v", p)
		}
	}
}
