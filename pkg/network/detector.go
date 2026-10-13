package network

import (
	"fmt"
	"net"
	"strings"

	"torrent-multipath/pkg/state"
)

// NetworkAdapter represents a physical active network interface ready for multi-path socket binding.
type NetworkAdapter struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	InterfaceName string            `json:"interfaceName"`
	IP            net.IP            `json:"ip"`
	HardwareAddr  string            `json:"hardwareAddr"`
	Type          state.AdapterType `json:"type"`
	Color         string            `json:"color"`
	IsUp          bool              `json:"isUp"`
}

// Palette of distinct modern colors assigned to adapters in dashboard views.
var adapterColors = []string{
	"#06B6D4", // Cyan
	"#10B981", // Emerald
	"#8B5CF6", // Violet
	"#F59E0B", // Amber
	"#EC4899", // Pink
	"#3B82F6", // Blue
}

// Blacklist substrings for virtual, hypervisor, or VPN interfaces.
var virtualInterfaceKeywords = []string{
	"vethernet", "virtual", "vmware", "vbox", "docker", "loopback",
	"tailscale", "zerotier", "wireguard", "wsl", "npcap", "tap",
	"tun", "teredo", "isatap", "bluetooth", "pseudo", "hyper-v",
}

// Detector scans, identifies, and categorizes physical network interfaces on the host.
type Detector struct{}

// NewDetector creates a new network interface detector.
func NewDetector() *Detector {
	return &Detector{}
}

// Scan finds all active, physical, non-loopback network adapters with valid IPv4 addresses.
func (d *Detector) Scan() ([]NetworkAdapter, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to query network interfaces: %w", err)
	}

	adapters := make([]NetworkAdapter, 0)
	colorIndex := 0

	for _, iface := range ifaces {
		// Filter 1: Interface must be UP
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		// Filter 2: Must NOT be loopback
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		// Filter 3: Exclude known virtual / tunnel adapters by name
		if isVirtualOrIgnored(iface.Name) {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet == nil {
				continue
			}

			ip := ipNet.IP.To4()
			if ip == nil {
				// We prioritize IPv4 for multipath socket binding to ensure remote peer compatibility
				continue
			}

			// Filter out loopback, multicast, or unspecified IPs
			if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}

			// Filter out APIPA / Link-local autoconfigured addresses (169.254.x.x)
			if ip[0] == 169 && ip[1] == 254 {
				continue
			}

			adapterType := classifyAdapterType(iface.Name)
			color := adapterColors[colorIndex%len(adapterColors)]
			colorIndex++

			friendlyName := formatFriendlyName(iface.Name, adapterType, ip)

			adapters = append(adapters, NetworkAdapter{
				ID:            fmt.Sprintf("%s-%s", iface.Name, ip.String()),
				Name:          friendlyName,
				InterfaceName: iface.Name,
				IP:            ip,
				HardwareAddr:  iface.HardwareAddr.String(),
				Type:          adapterType,
				Color:         color,
				IsUp:          true,
			})
		}
	}

	return adapters, nil
}

// isVirtualOrIgnored checks if the interface name belongs to a virtual or VPN bridge.
func isVirtualOrIgnored(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range virtualInterfaceKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// classifyAdapterType categorizes the adapter into WiFi, Ethernet, Cellular, or Other.
func classifyAdapterType(name string) state.AdapterType {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "wi-fi") || strings.Contains(lower, "wifi") ||
		strings.Contains(lower, "wlan") || strings.Contains(lower, "wireless") ||
		strings.Contains(lower, "802.11"):
		return state.AdapterTypeWiFi
	case strings.Contains(lower, "cellular") || strings.Contains(lower, "mobile") ||
		strings.Contains(lower, "wwan") || strings.Contains(lower, "lte") ||
		strings.Contains(lower, "usb") || strings.Contains(lower, "rndis") ||
		strings.Contains(lower, "tether"):
		return state.AdapterTypeCellular
	case strings.Contains(lower, "eth") || strings.Contains(lower, "ethernet") ||
		strings.Contains(lower, "lan") || strings.Contains(lower, "en"):
		return state.AdapterTypeEthernet
	default:
		return state.AdapterTypeOther
	}
}

// formatFriendlyName creates an intuitive display name for the dashboard UI.
func formatFriendlyName(ifaceName string, adapterType state.AdapterType, ip net.IP) string {
	typeLabel := "Network"
	switch adapterType {
	case state.AdapterTypeWiFi:
		typeLabel = "Wi-Fi"
	case state.AdapterTypeCellular:
		typeLabel = "Cellular / USB"
	case state.AdapterTypeEthernet:
		typeLabel = "Ethernet"
	}

	return fmt.Sprintf("%s (%s - %s)", typeLabel, ifaceName, ip.String())
}
