package common

import (
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSRFProtectionRejectsLiteralPrivateAndReservedIPs(t *testing.T) {
	protection := &SSRFProtection{
		AllowPrivateIp:   false,
		DomainFilterMode: false,
		IpFilterMode:     false,
	}

	tests := []struct {
		name string
		host string
	}{
		{name: "IPv4 unspecified", host: "0.0.0.0"},
		{name: "IPv4 loopback", host: "127.0.0.1"},
		{name: "IPv4 private", host: "10.0.0.1"},
		{name: "IPv4 carrier grade NAT", host: "100.64.0.1"},
		{name: "IPv4 link local", host: "169.254.169.254"},
		{name: "IPv4 multicast", host: "224.0.0.1"},
		{name: "IPv4 reserved", host: "240.0.0.1"},
		{name: "IPv4 documentation", host: "192.0.2.1"},
		{name: "IPv6 unspecified", host: "::"},
		{name: "IPv6 loopback", host: "::1"},
		{name: "IPv6 unique local", host: "fc00::1"},
		{name: "IPv6 link local", host: "fe80::1"},
		{name: "IPv6 multicast", host: "ff02::1"},
		{name: "IPv6 reserved", host: "100::1"},
		{name: "IPv6 documentation", host: "2001:db8::1"},
		{name: "IPv4 mapped IPv6", host: "::ffff:127.0.0.1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, protection.ValidateNetworkTarget(test.host, 80))
		})
	}
}

func TestSSRFProtectionAllowsPublicLiteralIPs(t *testing.T) {
	protection := &SSRFProtection{
		DomainFilterMode: false,
		IpFilterMode:     false,
	}

	for _, host := range []string{
		"8.8.8.8",
		"1.1.1.1",
		"2001:4860:4860::8888",
		"2606:4700:4700::1111",
	} {
		t.Run(host, func(t *testing.T) {
			require.NoError(t, protection.ValidateNetworkTarget(host, 443))
		})
	}
}

func TestSSRFProtectionAllowsPrivateIPWhenExplicitlyEnabled(t *testing.T) {
	protection := &SSRFProtection{
		AllowPrivateIp:   true,
		DomainFilterMode: false,
		IpFilterMode:     false,
	}

	require.NoError(t, protection.ValidateNetworkTarget("10.0.0.1", 80))
}

func TestSSRFProtectionRejectsScopedIPv6Literal(t *testing.T) {
	for _, applyIPFilterForDomain := range []bool{false, true} {
		t.Run(fmt.Sprintf("apply domain IP filter %t", applyIPFilterForDomain), func(t *testing.T) {
			protection := &SSRFProtection{
				DomainFilterMode:       false,
				IpFilterMode:           false,
				ApplyIPFilterForDomain: applyIPFilterForDomain,
			}

			err := protection.ValidateNetworkTarget("fe80::1%eth0", 80)
			require.Error(t, err)
			require.Contains(t, err.Error(), "private IP address not allowed")

			err = protection.ValidateURL("http://[fe80::1%25eth0]/")
			require.Error(t, err)
			require.Contains(t, err.Error(), "private IP address not allowed")
		})
	}
}

func TestSSRFProtectionAllowsScopedIPv6LiteralWhenPrivateIPExplicitlyEnabled(t *testing.T) {
	for _, applyIPFilterForDomain := range []bool{false, true} {
		t.Run(fmt.Sprintf("apply domain IP filter %t", applyIPFilterForDomain), func(t *testing.T) {
			protection := &SSRFProtection{
				AllowPrivateIp:         true,
				DomainFilterMode:       false,
				IpFilterMode:           false,
				ApplyIPFilterForDomain: applyIPFilterForDomain,
			}

			require.NoError(t, protection.ValidateNetworkTarget("fe80::1%eth0", 80))
			require.NoError(t, protection.ValidateURL("http://[fe80::1%25eth0]/"))
		})
	}
}

func TestSSRFProtectionRejectsResolvedPrivateIP(t *testing.T) {
	protection := &SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		ApplyIPFilterForDomain: true,
	}

	require.NoError(t, protection.ValidateNetworkTarget("example.com", 80))
	require.Error(t, protection.ValidateResolvedIP("example.com", net.ParseIP("169.254.169.254")))
}

func TestNewSSRFProtectionFromFetchSettingPreservesConfiguredFilters(t *testing.T) {
	protection, err := NewSSRFProtectionFromFetchSetting(
		false,
		true,
		true,
		[]string{"allowed.example"},
		[]string{"8.8.8.0/24"},
		[]string{"80", "8000-8001"},
		true,
	)
	require.NoError(t, err)

	require.NoError(t, protection.ValidateNetworkTarget("allowed.example", 8001))
	require.Error(t, protection.ValidateNetworkTarget("blocked.example", 80))
	require.NoError(t, protection.ValidateNetworkTarget("8.8.8.8", 80))
	require.Error(t, protection.ValidateNetworkTarget("1.1.1.1", 80))
	require.Error(t, protection.ValidateNetworkTarget("allowed.example", 9000))
}

func TestNewSSRFProtectionFromFetchSettingRejectsInvalidPortRanges(t *testing.T) {
	_, err := NewSSRFProtectionFromFetchSetting(false, false, false, nil, nil, []string{"9000-8000"}, true)
	require.Error(t, err)
}
