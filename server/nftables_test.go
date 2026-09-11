package server

import (
	"testing"
)

func TestConfigureNftables(t *testing.T) {
	ConfigureNftables(false, true)
	if nftablesLogging {
		t.Errorf("nftablesLogging = true, want false")
	}
	if !nftablesAcceptReplyTraffic {
		t.Errorf("nftablesAcceptReplyTraffic = false, want true")
	}

	// Reset to defaults
	ConfigureNftables(true, false)
	if !nftablesLogging {
		t.Errorf("nftablesLogging = false, want true")
	}
	if nftablesAcceptReplyTraffic {
		t.Errorf("nftablesAcceptReplyTraffic = true, want false")
	}
}

func TestNormalizeNftIPsAndMerge(t *testing.T) {
	rawIPs := []string{
		"192.168.1.1",
		"192.168.1.0/24",
		"2001:db8::1",
		"2001:db8::/32",
	}
	elems, err := normalizeNftIPs(rawIPs)
	if err != nil {
		t.Fatalf("normalizeNftIPs failed: %v", err)
	}
	if len(elems) != 2 {
		t.Fatalf("expected 2 merged elements (1 v4, 1 v6), got %d", len(elems))
	}
}
