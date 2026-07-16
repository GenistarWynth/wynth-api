package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

type staticSSRFResolver map[string][]net.IPAddr

type closeTrackingConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *closeTrackingConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		close(c.closed)
	})
	return err
}

func (r staticSSRFResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if ips, ok := r[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("unexpected lookup for %s", host)
}

func staticProtection(protection *common.SSRFProtection) func() (*common.SSRFProtection, bool, error) {
	return func() (*common.SSRFProtection, bool, error) {
		return protection, true, nil
	}
}

func testProtectedFetchConn(t *testing.T) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return clientConn
}

func testCloseTrackingConn(t *testing.T) *closeTrackingConn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	conn := &closeTrackingConn{
		Conn:   clientConn,
		closed: make(chan struct{}),
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = serverConn.Close()
	})
	return conn
}

func configureSSRFTestFetchSetting(t *testing.T) {
	t.Helper()
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() {
		*fetchSetting = original
	})

	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = []string{"80", "443"}
	fetchSetting.ApplyIPFilterForDomain = true
}

func mustParseProtectedFetchURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	require.NoError(t, err)
	return parsedURL
}

func TestProtectedFetchDialerRejectsPrivateReboundAddress(t *testing.T) {
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("127.0.0.1")}},
		},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			t.Fatalf("dialContext should not be called for blocked address %s", address)
			return nil, nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:80")
	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "private IP address not allowed")
}

func TestProtectedFetchDialerRejectsMixedResolvedIPs(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("10.0.0.1")},
				{IP: net.ParseIP("8.8.8.8")},
			},
		},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testProtectedFetchConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")
	require.Error(t, err)
	require.Nil(t, conn)
	require.Empty(t, dialed)
}

func TestProtectedFetchDialerRejectsBlockedAddressOutsideRequestedFamily(t *testing.T) {
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("fc00::1")},
				{IP: net.ParseIP("8.8.8.8")},
			},
		},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			t.Fatalf("dialContext should not be called when any DNS result is blocked: %s", address)
			return nil, nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp4", "safe.example:443")
	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "private IP address not allowed")
}

func TestProtectedFetchDialerDialsValidatedIPLiteral(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("1.1.1.1")},
			},
		},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testProtectedFetchConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []string{"8.8.8.8:443"}, dialed)
}

func TestProtectedFetchDialerFiltersIPsForRequestedNetwork(t *testing.T) {
	tests := []struct {
		name         string
		network      string
		expectedDial string
	}{
		{name: "tcp4", network: "tcp4", expectedDial: "8.8.8.8:443"},
		{name: "tcp6", network: "tcp6", expectedDial: "[2606:4700:4700::1111]:443"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var dialed []string
			dialer := &protectedFetchDialer{
				resolver: staticSSRFResolver{
					"safe.example": {
						{IP: net.ParseIP("2606:4700:4700::1111")},
						{IP: net.ParseIP("8.8.8.8")},
					},
				},
				dialContext: func(_ context.Context, network, address string) (net.Conn, error) {
					require.Equal(t, test.network, network)
					dialed = append(dialed, address)
					return testProtectedFetchConn(t), nil
				},
				getProtection: staticProtection(&common.SSRFProtection{
					DomainFilterMode:       false,
					IpFilterMode:           false,
					ApplyIPFilterForDomain: true,
				}),
			}

			conn, err := dialer.DialContext(context.Background(), test.network, "safe.example:443")
			require.NoError(t, err)
			require.NotNil(t, conn)
			require.Equal(t, []string{test.expectedDial}, dialed)
		})
	}
}

func TestProtectedFetchDialerRejectsWhenNoIPMatchesNetwork(t *testing.T) {
	tests := []struct {
		name    string
		network string
		ip      string
	}{
		{name: "tcp4 with only IPv6", network: "tcp4", ip: "2606:4700:4700::1111"},
		{name: "tcp6 with only IPv4", network: "tcp6", ip: "8.8.8.8"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dialer := &protectedFetchDialer{
				resolver: staticSSRFResolver{
					"safe.example": {{IP: net.ParseIP(test.ip)}},
				},
				dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
					t.Fatalf("dialContext should not be called for unusable address %s", address)
					return nil, nil
				},
				getProtection: staticProtection(&common.SSRFProtection{
					DomainFilterMode:       false,
					IpFilterMode:           false,
					ApplyIPFilterForDomain: true,
				}),
			}

			conn, err := dialer.DialContext(context.Background(), test.network, "safe.example:443")
			require.Error(t, err)
			require.Nil(t, conn)
			require.Contains(t, err.Error(), "no usable IP address")
		})
	}
}

func TestProtectedFetchDialerAllowsExplicitPrivateIPConfiguration(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"internal.example": {{IP: net.ParseIP("10.1.2.3")}},
		},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testProtectedFetchConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         true,
			DomainFilterMode:       false,
			IpFilterMode:           true,
			IpList:                 []string{"10.0.0.0/8"},
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "internal.example:80")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []string{"10.1.2.3:80"}, dialed)
}

func TestProtectedFetchDialerPreservesDisabledDomainIPFilterBehavior(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testProtectedFetchConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: false,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:80")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []string{"safe.example:80"}, dialed)
}

func TestProtectedFetchDialerDirectLiteralUsesProtectedDialPath(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testProtectedFetchConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode: false,
			IpFilterMode:     false,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "8.8.8.8:443")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []string{"8.8.8.8:443"}, dialed)
}

func TestProtectedFetchDialerRejectsScopedIPv6LiteralWhenDomainIPFilterDisabled(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testProtectedFetchConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: false,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp6", "[fe80::1%eth0]:80")
	require.Error(t, err)
	require.Nil(t, conn)
	require.Empty(t, dialed)
	require.Contains(t, err.Error(), "private IP address not allowed")
}

func TestProtectedFetchDialerPreservesScopedIPv6ZoneWhenPrivateIPExplicitlyEnabled(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testProtectedFetchConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         true,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: false,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp6", "[fe80::1%eth0]:80")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []string{"[fe80::1%eth0]:80"}, dialed)
}

func TestProtectedFetchDialerStartsFallbackAndClosesCanceledLoserConnection(t *testing.T) {
	firstStarted := make(chan struct{})
	firstCanceled := make(chan struct{})
	loser := testCloseTrackingConn(t)
	winner := testProtectedFetchConn(t)
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("1.1.1.1")},
			},
		},
		dialContext: func(ctx context.Context, _ string, address string) (net.Conn, error) {
			switch address {
			case "8.8.8.8:443":
				close(firstStarted)
				<-ctx.Done()
				close(firstCanceled)
				return loser, nil
			case "1.1.1.1:443":
				<-firstStarted
				return winner, nil
			default:
				return nil, fmt.Errorf("unexpected dial address %s", address)
			}
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
		fallbackDelay: func() time.Duration { return 0 },
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")
	require.NoError(t, err)
	require.Same(t, winner, conn)
	<-firstCanceled
	<-loser.closed
}

func TestProtectedFetchDialerImmediatelyTriesNextCandidateAfterFailure(t *testing.T) {
	firstErr := errors.New("first candidate failed")
	winner := testProtectedFetchConn(t)
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("1.1.1.1")},
			},
		},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			if address == "8.8.8.8:443" {
				return nil, firstErr
			}
			return winner, nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
		fallbackDelay: func() time.Duration { return time.Hour },
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")
	require.NoError(t, err)
	require.Same(t, winner, conn)
	require.Equal(t, []string{"8.8.8.8:443", "1.1.1.1:443"}, dialed)
}

func TestProtectedFetchDialerStopsBoundedAttemptsWhenParentContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan string, 3)
	stopped := make(chan string, 2)
	result := make(chan error, 1)
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("1.1.1.1")},
				{IP: net.ParseIP("9.9.9.9")},
			},
		},
		dialContext: func(ctx context.Context, _ string, address string) (net.Conn, error) {
			started <- address
			<-ctx.Done()
			stopped <- address
			return nil, ctx.Err()
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
		fallbackDelay: func() time.Duration { return 0 },
	}

	go func() {
		conn, err := dialer.DialContext(ctx, "tcp", "safe.example:443")
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()

	first := <-started
	second := <-started
	require.ElementsMatch(t, []string{"8.8.8.8:443", "1.1.1.1:443"}, []string{first, second})
	select {
	case address := <-started:
		t.Fatalf("more than two candidate dials started: %s", address)
	default:
	}

	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	<-stopped
	<-stopped
	select {
	case address := <-started:
		t.Fatalf("candidate dial started after parent cancellation: %s", address)
	default:
	}
}

func TestProtectedFetchDialerReturnsLastCandidateDialError(t *testing.T) {
	firstErr := errors.New("first candidate failed")
	lastErr := errors.New("last candidate failed")
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("1.1.1.1")},
			},
		},
		dialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			if address == "8.8.8.8:443" {
				return nil, firstErr
			}
			return nil, lastErr
		},
		getProtection: staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
		fallbackDelay: func() time.Duration { return 0 },
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")
	require.Nil(t, conn)
	require.ErrorIs(t, err, lastErr)
}

func TestGetSSRFProtectedHTTPClientFallsBackWhenProtectionDisabled(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	originalHTTPClient := httpClient
	originalProtectedClient := ssrfProtectedHTTPClient
	t.Cleanup(func() {
		*fetchSetting = originalFetchSetting
		httpClient = originalHTTPClient
		ssrfProtectedHTTPClient = originalProtectedClient
	})

	fetchSetting.EnableSSRFProtection = false
	expected := &http.Client{}
	httpClient = expected
	ssrfProtectedHTTPClient = &http.Client{}

	require.Same(t, expected, GetSSRFProtectedHTTPClient())
}

func TestCheckProtectedFetchRedirectValidatesEveryHopAndLimitsRedirects(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	publicRequest := &http.Request{URL: mustParseProtectedFetchURL(t, "http://8.8.8.8/next")}
	privateRequest := &http.Request{URL: mustParseProtectedFetchURL(t, "http://127.0.0.1/blocked")}

	require.NoError(t, checkProtectedFetchRedirect(publicRequest, nil))
	require.Error(t, checkProtectedFetchRedirect(privateRequest, []*http.Request{publicRequest}))

	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = publicRequest
	}
	err := checkProtectedFetchRedirect(publicRequest, via)
	require.EqualError(t, err, "stopped after 10 redirects")
}

func TestProtectedFetchRoundTripperUsesConfiguredHTTPProxy(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	proxyURL := mustParseProtectedFetchURL(t, "http://127.0.0.1:3128")
	var dialed []string
	client := newProtectedFetchHTTPClientWithProxy(
		staticSSRFResolver{},
		func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("stop after proxy dial")
		},
		staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
		func(_ *http.Request) (*url.URL, error) {
			return proxyURL, nil
		},
	)
	req, err := http.NewRequest(http.MethodGet, "http://8.8.8.8/resource", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, []string{"127.0.0.1:3128"}, dialed)
}

func TestProtectedFetchRoundTripperRejectsBlockedDestinationBeforeProxyDial(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	proxyURL := mustParseProtectedFetchURL(t, "http://127.0.0.1:3128")
	var dialed []string
	client := newProtectedFetchHTTPClientWithProxy(
		staticSSRFResolver{},
		func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("proxy should not be dialed")
		},
		staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
		func(_ *http.Request) (*url.URL, error) {
			return proxyURL, nil
		},
	)
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/resource", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Contains(t, err.Error(), "private IP address not allowed")
	require.Empty(t, dialed)
}

func TestProtectedFetchRoundTripperDirectRequestUsesValidatedLiteral(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	var dialed []string
	client := newProtectedFetchHTTPClientWithProxy(
		staticSSRFResolver{},
		func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("stop after direct dial")
		},
		staticProtection(&common.SSRFProtection{
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
		func(_ *http.Request) (*url.URL, error) {
			return nil, nil
		},
	)
	req, err := http.NewRequest(http.MethodGet, "http://8.8.8.8/resource", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, []string{"8.8.8.8:80"}, dialed)
}

func TestProtectedFetchRoundTripperReusesTransportByDirectOrProxyURL(t *testing.T) {
	client := newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
	roundTripper, ok := client.Transport.(*ssrfProtectedRoundTripper)
	require.True(t, ok)

	direct := roundTripper.transportFor(nil)
	directAgain := roundTripper.transportFor(nil)
	proxyURL := mustParseProtectedFetchURL(t, "http://127.0.0.1:3128")
	proxied := roundTripper.transportFor(proxyURL)
	proxiedAgain := roundTripper.transportFor(proxyURL)
	otherProxy := roundTripper.transportFor(mustParseProtectedFetchURL(t, "http://127.0.0.1:3129"))

	require.Same(t, direct, directAgain)
	require.Same(t, proxied, proxiedAgain)
	require.NotSame(t, direct, proxied)
	require.NotSame(t, proxied, otherProxy)
	require.True(t, direct.ForceAttemptHTTP2)
	require.False(t, direct.DisableKeepAlives)
}

func TestProtectedFetchClientDoesNotReplaceOrdinaryProviderClients(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	originalHTTPClient := httpClient
	originalProtectedClient := ssrfProtectedHTTPClient
	proxyClientLock.Lock()
	originalProxyClients := proxyClients
	proxyClients = make(map[string]*http.Client)
	proxyClientLock.Unlock()
	t.Cleanup(func() {
		httpClient = originalHTTPClient
		ssrfProtectedHTTPClient = originalProtectedClient
		ResetProxyClientCache()
		proxyClientLock.Lock()
		proxyClients = originalProxyClients
		proxyClientLock.Unlock()
	})

	InitHttpClient()
	ordinary := GetHttpClient()
	protected := GetSSRFProtectedHTTPClient()
	require.NotSame(t, ordinary, protected)
	ordinaryTransport, ok := ordinary.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, ordinaryTransport.DialContext)

	proxyClient, err := NewProxyHttpClient("http://127.0.0.1:3128")
	require.NoError(t, err)
	_, isProtected := proxyClient.Transport.(*ssrfProtectedRoundTripper)
	require.False(t, isProtected)
}
