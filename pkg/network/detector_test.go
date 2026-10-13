package network

import (
	"testing"
)

func TestDetectorScan(t *testing.T) {
	detector := NewDetector()
	adapters, err := detector.Scan()
	if err != nil {
		t.Fatalf("Detector.Scan failed: %v", err)
	}

	for _, a := range adapters {
		if a.IP == nil {
			t.Errorf("adapter %s has nil IP", a.Name)
		}
		if a.IP.To4() == nil {
			t.Errorf("adapter %s IP %s is not IPv4", a.Name, a.IP.String())
		}
		if a.IP.IsLoopback() {
			t.Errorf("adapter %s IP %s is loopback", a.Name, a.IP.String())
		}
		if a.Color == "" {
			t.Errorf("adapter %s has empty color", a.Name)
		}
	}
}

func TestClassifyAdapterType(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{"Wi-Fi", "wifi"},
		{"wlan0", "wifi"},
		{"Wireless Network Connection", "wifi"},
		{"Cellular", "cellular"},
		{"Mobile Broadband", "cellular"},
		{"Ethernet", "ethernet"},
		{"eth0", "ethernet"},
		{"en0", "ethernet"},
	}

	for _, tt := range tests {
		actual := string(classifyAdapterType(tt.name))
		if actual != tt.expected {
			t.Errorf("classifyAdapterType(%q) = %s; want %s", tt.name, actual, tt.expected)
		}
	}
}

func TestIsVirtualOrIgnored(t *testing.T) {
	if !isVirtualOrIgnored("vEthernet (WSL)") {
		t.Errorf("expected vEthernet to be ignored")
	}
	if !isVirtualOrIgnored("docker0") {
		t.Errorf("expected docker0 to be ignored")
	}
	if !isVirtualOrIgnored("tailscale0") {
		t.Errorf("expected tailscale0 to be ignored")
	}
	if isVirtualOrIgnored("Wi-Fi") {
		t.Errorf("Wi-Fi should not be ignored")
	}
	if isVirtualOrIgnored("Ethernet") {
		t.Errorf("Ethernet should not be ignored")
	}
}
