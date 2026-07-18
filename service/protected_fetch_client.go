package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

type ssrfResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

const protectedFetchFallbackDelay = 300 * time.Millisecond

type protectedFetchDialResult struct {
	index int
	conn  net.Conn
	err   error
}

type protectedFetchDialer struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
	fallbackDelay func() time.Duration
}

type ssrfProtectedRoundTripper struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
	proxy         func(*http.Request) (*url.URL, error)

	mutex      sync.Mutex
	transports map[string]*http.Transport
}

func currentFetchProtection() (*common.SSRFProtection, bool, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil, false, nil
	}

	protection, err := common.NewSSRFProtectionFromFetchSetting(
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	)
	if err != nil {
		return nil, true, err
	}
	return protection, true, nil
}

func newProtectedFetchHTTPClient() *http.Client {
	return newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
}

func newProtectedFetchHTTPClientWithDialer(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error)) *http.Client {
	return newProtectedFetchHTTPClientWithProxy(resolver, dialContext, getProtection, http.ProxyFromEnvironment)
}

func newProtectedFetchHTTPClientWithProxy(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error), proxy func(*http.Request) (*url.URL, error)) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialContext == nil {
		netDialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		dialContext = netDialer.DialContext
	}
	if getProtection == nil {
		getProtection = currentFetchProtection
	}
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}

	client := &http.Client{
		Transport: &ssrfProtectedRoundTripper{
			resolver:      resolver,
			dialContext:   dialContext,
			getProtection: getProtection,
			proxy:         proxy,
			transports:    make(map[string]*http.Transport),
		},
		CheckRedirect: checkProtectedFetchRedirect,
	}
	if common.RelayTimeout != 0 {
		client.Timeout = time.Duration(common.RelayTimeout) * time.Second
	}
	return client
}

func (t *ssrfProtectedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("invalid request")
	}
	if err := ValidateSSRFProtectedFetchURL(req.URL.String()); err != nil {
		return nil, err
	}

	proxyURL, err := t.proxy(req)
	if err != nil {
		return nil, err
	}
	return t.transportFor(proxyURL).RoundTrip(req)
}

func (t *ssrfProtectedRoundTripper) CloseIdleConnections() {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	for _, transport := range t.transports {
		transport.CloseIdleConnections()
	}
}

func (t *ssrfProtectedRoundTripper) transportFor(proxyURL *url.URL) *http.Transport {
	// The proxy decision has bounded operator-controlled values. Request origins
	// are intentionally excluded from the cache key.
	key := "direct"
	if proxyURL != nil {
		key = proxyURL.String()
	}

	t.mutex.Lock()
	defer t.mutex.Unlock()
	if transport, ok := t.transports[key]; ok {
		return transport
	}

	transport := t.newTransport(proxyURL)
	t.transports[key] = transport
	return transport
}

func (t *ssrfProtectedRoundTripper) newTransport(proxyURL *url.URL) *http.Transport {
	dialContext := t.dialContext
	proxyFunc := http.ProxyURL(proxyURL)
	if proxyURL == nil {
		protectedDialer := &protectedFetchDialer{
			resolver:      t.resolver,
			dialContext:   t.dialContext,
			getProtection: t.getProtection,
		}
		dialContext = protectedDialer.DialContext
		proxyFunc = nil
	} else {
		// An operator-configured forward proxy is an explicit trust boundary.
		// RoundTrip validates the destination before this transport is selected,
		// while the proxy endpoint itself may be private. Remote destination
		// resolution must be performed by a trusted proxy with equivalent policy.
	}

	transport := &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:   true,
		Proxy:               proxyFunc,
		DialContext:         dialContext,
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig
	}
	return transport
}

func (d *protectedFetchDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	protection, enabled, err := d.getProtection()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return d.dialContext(ctx, network, addr)
	}

	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %s: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %s", portText)
	}
	if err := protection.ValidateNetworkTarget(host, port); err != nil {
		return nil, err
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		return d.dialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), portText))
	}
	if !protection.ApplyIPFilterForDomain {
		return d.dialContext(ctx, network, addr)
	}

	resolved, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %v", host, err)
	}

	candidateIPs := make([]net.IP, 0, len(resolved))
	for _, ipAddr := range resolved {
		ip := ipAddr.IP
		if ip == nil {
			continue
		}
		// Validate every answer before selecting an address family so mixed DNS
		// results fail closed even when a blocked answer would not be dialed.
		if err := protection.ValidateResolvedIP(host, ip); err != nil {
			return nil, err
		}
		if networkAllowsIP(network, ip) {
			candidateIPs = append(candidateIPs, ip)
		}
	}

	if len(candidateIPs) == 0 {
		return nil, fmt.Errorf("DNS resolution for %s returned no usable IP addresses", host)
	}
	return d.dialCandidateIPs(ctx, network, portText, candidateIPs)
}

func (d *protectedFetchDialer) dialCandidateIPs(ctx context.Context, network, portText string, candidateIPs []net.IP) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan protectedFetchDialResult, 2)
	errorsByCandidate := make([]error, len(candidateIPs))
	nextCandidate := 0
	inFlight := 0

	startCandidate := func() {
		index := nextCandidate
		nextCandidate++
		inFlight++
		address := net.JoinHostPort(candidateIPs[index].String(), portText)
		go func() {
			conn, err := d.dialContext(dialCtx, network, address)
			results <- protectedFetchDialResult{index: index, conn: conn, err: err}
		}()
	}

	cleanupPending := func(count int) {
		if count == 0 {
			return
		}
		go func() {
			for i := 0; i < count; i++ {
				result := <-results
				if result.conn != nil {
					_ = result.conn.Close()
				}
			}
		}()
	}

	var fallbackTimer *time.Timer
	var fallbackC <-chan time.Time
	stopFallback := func() {
		if fallbackTimer == nil {
			return
		}
		if !fallbackTimer.Stop() {
			select {
			case <-fallbackTimer.C:
			default:
			}
		}
		fallbackTimer = nil
		fallbackC = nil
	}
	scheduleFallback := func() {
		if fallbackC != nil || inFlight == 0 || inFlight >= 2 || nextCandidate >= len(candidateIPs) {
			return
		}
		delay := protectedFetchFallbackDelay
		if d.fallbackDelay != nil {
			delay = d.fallbackDelay()
		}
		if delay < 0 {
			delay = 0
		}
		fallbackTimer = time.NewTimer(delay)
		fallbackC = fallbackTimer.C
	}
	defer stopFallback()

	startCandidate()
	scheduleFallback()
	for {
		select {
		case <-ctx.Done():
			cancel()
			stopFallback()
			cleanupPending(inFlight)
			return nil, ctx.Err()
		case <-fallbackC:
			fallbackTimer = nil
			fallbackC = nil
			if ctx.Err() != nil {
				cancel()
				cleanupPending(inFlight)
				return nil, ctx.Err()
			}
			startCandidate()
			scheduleFallback()
		case result := <-results:
			inFlight--
			if ctx.Err() != nil {
				if result.conn != nil {
					_ = result.conn.Close()
				}
				cancel()
				stopFallback()
				cleanupPending(inFlight)
				return nil, ctx.Err()
			}
			if result.err == nil && result.conn != nil {
				cancel()
				stopFallback()
				cleanupPending(inFlight)
				return result.conn, nil
			}
			if result.conn != nil {
				_ = result.conn.Close()
			}
			if result.err == nil {
				result.err = fmt.Errorf("destination candidate dial returned no connection")
			}
			errorsByCandidate[result.index] = result.err

			if nextCandidate < len(candidateIPs) {
				stopFallback()
				startCandidate()
			}
			if inFlight == 0 && nextCandidate == len(candidateIPs) {
				for i := len(errorsByCandidate) - 1; i >= 0; i-- {
					if errorsByCandidate[i] != nil {
						return nil, errorsByCandidate[i]
					}
				}
			}
			scheduleFallback()
		}
	}
}

func networkAllowsIP(network string, ip net.IP) bool {
	switch network {
	case "tcp4":
		return ip.To4() != nil
	case "tcp6":
		return ip.To4() == nil
	default:
		return true
	}
}
