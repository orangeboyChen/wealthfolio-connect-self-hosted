package okx

import "testing"

func TestChainName_AllBranches(t *testing.T) {
	cases := map[string]string{
		"1":     "ETHEREUM",
		"56":    "BSC",
		"137":   "POLYGON",
		"42161": "ARBITRUM",
		"10":    "OPTIMISM",
		"8453":  "BASE",
		"43114": "AVALANCHE",
		"324":   "ZKSYNC",
		"59144": "LINEA",
		"5000":  "MANTLE",
		"9999":  "CHAIN-9999", // default
	}
	for in, want := range cases {
		if got := chainName(in); got != want {
			t.Errorf("chainName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortAddr(t *testing.T) {
	if shortAddr("0x12") != "0x12" {
		t.Error("short address should pass through unchanged")
	}
	if got := shortAddr("0xabcdef0123456789"); got == "0xabcdef0123456789" {
		t.Error("long address should be truncated")
	}
}
