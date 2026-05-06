package collector

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestParseNetDevAllInterfaces(t *testing.T) {
	t.Parallel()
	stats, err := parseNetDev(bytes.NewReader(loadFixture(t, "proc/net_dev_t1")))
	if err != nil {
		t.Fatalf("parseNetDev: %v", err)
	}
	want := map[string]struct {
		rx, tx uint64
	}{
		"lo":          {31674304, 31674304},
		"enp1s0f1":    {762076152, 245679064},
		"wlp2s0":      {0, 0},
		"tailscale0":  {13585369, 60612549},
		"docker0":     {742911, 972809},
		"vethd434030": {759599, 972809},
	}
	for name, w := range want {
		got, ok := stats[name]
		if !ok {
			t.Errorf("interface %s missing from output", name)
			continue
		}
		if got.rxBytes != w.rx {
			t.Errorf("%s rx = %d, want %d", name, got.rxBytes, w.rx)
		}
		if got.txBytes != w.tx {
			t.Errorf("%s tx = %d, want %d", name, got.txBytes, w.tx)
		}
	}
}

func TestParseNetDevSkipsHeader(t *testing.T) {
	t.Parallel()
	input := "Inter-|   Receive                                |  Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n"
	stats, err := parseNetDev(bytes.NewReader([]byte(input)))
	if err != nil {
		t.Fatalf("parseNetDev: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("header-only input must yield no interfaces, got %d", len(stats))
	}
}

func TestReadOperstateUp(t *testing.T) {
	t.Parallel()
	state, err := readOperstate("testdata/sys", "enp1s0f1")
	if err != nil {
		t.Fatalf("readOperstate: %v", err)
	}
	if state != "up" {
		t.Errorf("got %q, want \"up\"", state)
	}
}

func TestReadOperstateMissing(t *testing.T) {
	t.Parallel()
	if _, err := readOperstate("testdata/sys", "nope0"); err == nil {
		t.Error("expected error for missing interface")
	}
}

func TestIsInterfaceUp(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"up":      true,
		"unknown": true, // tunnels report unknown but are up
		"down":    false,
		"":        false,
	}
	for state, want := range cases {
		if got := isInterfaceUp(state); got != want {
			t.Errorf("isInterfaceUp(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestNetworkCollectorReturnsConfiguredIfacesInOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "net", "dev"), []byte{}, 0644); err != nil {
		// Ensure dir hierarchy exists first.
		_ = os.MkdirAll(filepath.Join(dir, "net"), 0755)
		if err := os.WriteFile(filepath.Join(dir, "net", "dev"), loadFixture(t, "proc/net_dev_t1"), 0644); err != nil {
			t.Fatal(err)
		}
	} else {
		_ = os.WriteFile(filepath.Join(dir, "net", "dev"), loadFixture(t, "proc/net_dev_t1"), 0644)
	}
	want := []string{"enp1s0f1", "tailscale0", "wlp2s0"}
	c := NewNetworkCollector(dir, "testdata/sys", want)
	ifaces, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(ifaces) != len(want) {
		t.Fatalf("got %d ifaces, want %d", len(ifaces), len(want))
	}
	for i, name := range want {
		if ifaces[i].Name != name {
			t.Errorf("ifaces[%d].Name = %q, want %q", i, ifaces[i].Name, name)
		}
	}
	// First call: rates must be 0.
	for _, iface := range ifaces {
		if iface.RxBytesPerSec != 0 || iface.TxBytesPerSec != 0 {
			t.Errorf("first-call rate non-zero for %s: %+v", iface.Name, iface)
		}
	}
}

func TestNetworkCollectorComputesRatesAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	netDir := filepath.Join(dir, "net")
	if err := os.MkdirAll(netDir, 0755); err != nil {
		t.Fatal(err)
	}
	devFile := filepath.Join(netDir, "dev")
	if err := os.WriteFile(devFile, loadFixture(t, "proc/net_dev_t1"), 0644); err != nil {
		t.Fatal(err)
	}
	c := NewNetworkCollector(dir, "testdata/sys", []string{"enp1s0f1"})
	if _, err := c.Collect(); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(devFile, loadFixture(t, "proc/net_dev_t2"), 0644); err != nil {
		t.Fatal(err)
	}
	ifaces, err := c.Collect()
	if err != nil {
		t.Fatalf("second Collect: %v", err)
	}
	if len(ifaces) != 1 || ifaces[0].Name != "enp1s0f1" {
		t.Fatalf("unexpected ifaces: %+v", ifaces)
	}
	if ifaces[0].RxBytesPerSec <= 0 {
		t.Errorf("RxBytesPerSec = %f, want > 0", ifaces[0].RxBytesPerSec)
	}
	if ifaces[0].TxBytesPerSec <= 0 {
		t.Errorf("TxBytesPerSec = %f, want > 0", ifaces[0].TxBytesPerSec)
	}
	if ifaces[0].RxBytes != 762200000 {
		t.Errorf("RxBytes = %d, want 762200000", ifaces[0].RxBytes)
	}
	if !ifaces[0].IsUp {
		t.Error("enp1s0f1 must be marked up")
	}
}

func TestNetworkCollectorReturnsAllWhenIfacesNil(t *testing.T) {
	dir := t.TempDir()
	netDir := filepath.Join(dir, "net")
	if err := os.MkdirAll(netDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netDir, "dev"), loadFixture(t, "proc/net_dev_t1"), 0644); err != nil {
		t.Fatal(err)
	}
	c := NewNetworkCollector(dir, "testdata/sys", nil)
	ifaces, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	names := make([]string, 0, len(ifaces))
	for _, i := range ifaces {
		names = append(names, i.Name)
	}
	sort.Strings(names)
	for _, want := range []string{"docker0", "enp1s0f1", "lo", "tailscale0", "vethd434030", "wlp2s0"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing iface %s in output (got %v)", want, names)
		}
	}
}

func TestNetworkCollectorErrorsOnMissingNetDev(t *testing.T) {
	t.Parallel()
	c := NewNetworkCollector(t.TempDir(), "testdata/sys", []string{"enp1s0f1"})
	if _, err := c.Collect(); err == nil {
		t.Error("expected error for missing /proc/net/dev")
	}
}

func TestNetworkCollectorAgainstLiveProc(t *testing.T) {
	if _, err := os.Stat("/proc/net/dev"); err != nil {
		t.Skip("no live /proc/net/dev")
	}
	c := NewNetworkCollector("/proc", "/sys", []string{"lo"})
	ifaces, err := c.Collect()
	if err != nil {
		t.Fatalf("live Collect: %v", err)
	}
	if len(ifaces) != 1 || ifaces[0].Name != "lo" {
		t.Errorf("unexpected: %+v", ifaces)
	}
}
