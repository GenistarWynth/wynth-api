package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"gorm.io/gorm"
)

var (
	errUpstreamSourceBillingProbeInvalidURL      = errors.New("invalid upstream billing probe base URL")
	errUpstreamSourceBillingProbeInvalidResponse = errors.New("invalid upstream billing probe response")
	errUpstreamSourceBillingProbeRedirect        = errors.New("upstream billing probe redirects are prohibited")

	ErrUpstreamSourceBillingProbeNotEnabled       = errors.New("upstream billing probe is not enabled")
	ErrUpstreamSourceBillingProbeLeaseUnavailable = errors.New("upstream billing probe lease is unavailable")
	ErrUpstreamSourceBillingProbeIdentityChanged  = errors.New("upstream billing probe identity changed")
	ErrUpstreamSourceBillingProbePanicked         = errors.New("upstream billing probe panicked")
)

type upstreamSourceBillingProbeIdentityError struct {
	code string
}

func (err *upstreamSourceBillingProbeIdentityError) Error() string {
	if err == nil || err.code == "" {
		return UpstreamSourceBillingProbeErrorIneligible
	}
	return err.code
}

type upstreamSourceBillingProbeFetchPolicy struct {
	Enabled                bool     `json:"enabled"`
	AllowPrivateIP         bool     `json:"allow_private_ip"`
	DomainFilterMode       bool     `json:"domain_filter_mode"`
	IPFilterMode           bool     `json:"ip_filter_mode"`
	DomainList             []string `json:"domain_list"`
	IPList                 []string `json:"ip_list"`
	AllowedPorts           []string `json:"allowed_ports"`
	ApplyIPFilterForDomain bool     `json:"apply_ip_filter_for_domain"`
}

type upstreamSourceBillingProbeRequestIdentity struct {
	SourceID        int
	MappingID       int
	ChannelID       int
	BearerKey       string
	BaseURL         string
	RequestURL      string
	ProxyURL        *url.URL
	HeaderOverride  string
	TransportPolicy HTTPTransportPolicy
	FetchPolicy     upstreamSourceBillingProbeFetchPolicy
	Fingerprint     string
}

type upstreamSourceBillingProbeChannelOwnership struct {
	GeneratedByUpstreamSourceID  int `json:"generated_by_upstream_source_id"`
	GeneratedByUpstreamMappingID int `json:"generated_by_upstream_mapping_id"`
}

type upstreamSourceBillingProbeFingerprint struct {
	SourceID         int                                        `json:"source_id"`
	SourceType       string                                     `json:"source_type"`
	SourceStatus     string                                     `json:"source_status"`
	SourceRelayURL   string                                     `json:"source_relay_url"`
	SourceSyncConfig string                                     `json:"source_sync_config"`
	MappingID        int                                        `json:"mapping_id"`
	MappingSourceID  int                                        `json:"mapping_source_id"`
	MappingSync      bool                                       `json:"mapping_sync"`
	MappingDiscovery string                                     `json:"mapping_discovery"`
	UpstreamKeyID    string                                     `json:"upstream_key_id"`
	LocalChannelID   int                                        `json:"local_channel_id"`
	ChannelType      int                                        `json:"channel_type"`
	ChannelStatus    int                                        `json:"channel_status"`
	ChannelBaseURL   string                                     `json:"channel_base_url"`
	ChannelSetting   string                                     `json:"channel_setting"`
	ChannelHeaders   string                                     `json:"channel_headers"`
	ChannelOwnership upstreamSourceBillingProbeChannelOwnership `json:"channel_ownership"`
	BearerKeySHA256  string                                     `json:"bearer_key_sha256"`
	ProbeEnabled     bool                                       `json:"probe_enabled"`
	ProbeInterval    int                                        `json:"probe_interval"`
	RequestURL       string                                     `json:"request_url"`
	TransportPolicy  string                                     `json:"transport_policy"`
	FetchPolicy      upstreamSourceBillingProbeFetchPolicy      `json:"fetch_policy"`
}

func loadUpstreamSourceBillingProbeIdentity(ctx context.Context, db *gorm.DB, sourceID int, mappingID int) (upstreamSourceBillingProbeRequestIdentity, *upstreamSourceBillingProbeIdentityError) {
	unlock := lockUpstreamSourceBillingProbeDatabase()
	defer unlock()
	if db == nil {
		db = model.DB
	}
	var probe model.UpstreamSourceBillingProbe
	if err := db.WithContext(ctx).Where("source_id = ? AND mapping_id = ?", sourceID, mappingID).First(&probe).Error; err != nil {
		return upstreamSourceBillingProbeRequestIdentity{}, &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorIneligible}
	}
	var source model.UpstreamSource
	if err := db.WithContext(ctx).Where("id = ?", sourceID).First(&source).Error; err != nil {
		return upstreamSourceBillingProbeRequestIdentity{}, &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorIneligible}
	}
	var mapping model.UpstreamSourceChannelMapping
	if err := db.WithContext(ctx).Where("id = ? AND source_id = ?", mappingID, sourceID).First(&mapping).Error; err != nil {
		return upstreamSourceBillingProbeRequestIdentity{}, &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorIneligible}
	}
	var channel model.Channel
	if mapping.LocalChannelID != 0 {
		if err := db.WithContext(ctx).Where("id = ?", mapping.LocalChannelID).First(&channel).Error; err != nil {
			return upstreamSourceBillingProbeRequestIdentity{}, &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorIneligible}
		}
	}
	return upstreamSourceBillingProbeIdentityFromRows(&probe, &source, &mapping, &channel)
}

func upstreamSourceBillingProbeIdentityFromRows(
	probe *model.UpstreamSourceBillingProbe,
	source *model.UpstreamSource,
	mapping *model.UpstreamSourceChannelMapping,
	channel *model.Channel,
) (upstreamSourceBillingProbeRequestIdentity, *upstreamSourceBillingProbeIdentityError) {
	ineligible := func() (upstreamSourceBillingProbeRequestIdentity, *upstreamSourceBillingProbeIdentityError) {
		return upstreamSourceBillingProbeRequestIdentity{}, &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorIneligible}
	}
	if probe == nil || source == nil || mapping == nil || channel == nil ||
		!probe.Enabled || probe.SourceID != source.Id || probe.MappingID != mapping.Id ||
		probe.IntervalMinutes < model.MinUpstreamSourceBillingProbeIntervalMinutes ||
		probe.IntervalMinutes > model.MaxUpstreamSourceBillingProbeIntervalMinutes ||
		source.Type != model.UpstreamSourceTypeSub2API || source.Status != model.UpstreamSourceStatusEnabled ||
		mapping.SourceID != source.Id || !mapping.SyncEnabled ||
		mapping.DiscoveryStatus != model.UpstreamMappingDiscoveryStatusActive ||
		strings.TrimSpace(mapping.UpstreamKeyID) == "" || mapping.LocalChannelID == 0 ||
		channel.Id != mapping.LocalChannelID || channel.Status != common.ChannelStatusEnabled ||
		!model.IsSupportedUpstreamSourceBillingProbeChannelType(channel.Type) {
		return ineligible()
	}

	var ownership relaydto.ChannelOtherSettings
	if strings.TrimSpace(channel.OtherSettings) == "" || common.UnmarshalJsonStr(channel.OtherSettings, &ownership) != nil ||
		!isGeneratedChannelMetadataMatching(&ownership, source.Id, mapping.Id) {
		return ineligible()
	}
	if channel.ChannelInfo.IsMultiKey {
		return ineligible()
	}
	if containsUpstreamSourceBillingProbeLiteralControl(channel.Key) {
		return ineligible()
	}
	bearerKey := strings.TrimSpace(channel.Key)
	if bearerKey == "" || strings.HasPrefix(bearerKey, "[") {
		return ineligible()
	}

	channelBaseURL := strings.TrimSpace(channel.GetBaseURL())
	sourceRelayURL := strings.TrimSpace(upstreamSourceGeneratedBaseURL(source))
	if channelBaseURL == "" || sourceRelayURL == "" ||
		strings.TrimRight(channelBaseURL, "/") != strings.TrimRight(sourceRelayURL, "/") {
		return ineligible()
	}
	requestURL, err := buildUpstreamSourceBillingURL(channelBaseURL)
	if err != nil {
		return upstreamSourceBillingProbeRequestIdentity{}, &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorInvalidBaseURL}
	}

	var channelSettings relaydto.ChannelSettings
	channelSettingRaw := ""
	if channel.Setting != nil {
		channelSettingRaw = *channel.Setting
	}
	if strings.TrimSpace(channelSettingRaw) != "" && common.UnmarshalJsonStr(channelSettingRaw, &channelSettings) != nil {
		return ineligible()
	}
	proxyURL, err := common.ParseProxyURLStrict(channelSettings.Proxy)
	if err != nil {
		return upstreamSourceBillingProbeRequestIdentity{}, &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorInvalidProxy}
	}

	config, err := parseUpstreamSourceSyncConfig(source.SyncConfig)
	if err != nil {
		return ineligible()
	}
	fetchSetting := system_setting.GetFetchSetting()
	fetchPolicy := upstreamSourceBillingProbeFetchPolicy{}
	if fetchSetting != nil {
		fetchPolicy = upstreamSourceBillingProbeFetchPolicy{
			Enabled:                fetchSetting.EnableSSRFProtection,
			AllowPrivateIP:         fetchSetting.AllowPrivateIp || bool(config.AllowPrivateIP),
			DomainFilterMode:       fetchSetting.DomainFilterMode,
			IPFilterMode:           fetchSetting.IpFilterMode,
			DomainList:             append([]string(nil), fetchSetting.DomainList...),
			IPList:                 append([]string(nil), fetchSetting.IpList...),
			AllowedPorts:           append([]string(nil), fetchSetting.AllowedPorts...),
			ApplyIPFilterForDomain: fetchSetting.ApplyIPFilterForDomain,
		}
	}
	headerOverride := ""
	if channel.HeaderOverride != nil {
		headerOverride = *channel.HeaderOverride
	}
	if err := applyUpstreamSourceBillingProbeHeaderOverrides(http.Header{}, headerOverride, bearerKey); err != nil {
		return ineligible()
	}
	keyDigest := sha256.Sum256([]byte(bearerKey))
	fingerprintData := upstreamSourceBillingProbeFingerprint{
		SourceID:         source.Id,
		SourceType:       source.Type,
		SourceStatus:     source.Status,
		SourceRelayURL:   source.RelayBaseURL,
		SourceSyncConfig: source.SyncConfig,
		MappingID:        mapping.Id,
		MappingSourceID:  mapping.SourceID,
		MappingSync:      mapping.SyncEnabled,
		MappingDiscovery: mapping.DiscoveryStatus,
		UpstreamKeyID:    mapping.UpstreamKeyID,
		LocalChannelID:   mapping.LocalChannelID,
		ChannelType:      channel.Type,
		ChannelStatus:    channel.Status,
		ChannelBaseURL:   channelBaseURL,
		ChannelSetting:   channelSettingRaw,
		ChannelHeaders:   headerOverride,
		ChannelOwnership: upstreamSourceBillingProbeChannelOwnership{
			GeneratedByUpstreamSourceID:  ownership.GeneratedByUpstreamSourceID,
			GeneratedByUpstreamMappingID: ownership.GeneratedByUpstreamMappingID,
		},
		BearerKeySHA256: fmt.Sprintf("%x", keyDigest),
		ProbeEnabled:    probe.Enabled,
		ProbeInterval:   probe.IntervalMinutes,
		RequestURL:      requestURL,
		TransportPolicy: NormalizeHTTPTransportPolicy(channelSettings).String(),
		FetchPolicy:     fetchPolicy,
	}
	fingerprintJSON, err := common.Marshal(fingerprintData)
	if err != nil {
		return ineligible()
	}
	fingerprintDigest := sha256.Sum256(fingerprintJSON)
	return upstreamSourceBillingProbeRequestIdentity{
		SourceID:        source.Id,
		MappingID:       mapping.Id,
		ChannelID:       channel.Id,
		BearerKey:       bearerKey,
		BaseURL:         channelBaseURL,
		RequestURL:      requestURL,
		ProxyURL:        proxyURL,
		HeaderOverride:  headerOverride,
		TransportPolicy: NormalizeHTTPTransportPolicy(channelSettings),
		FetchPolicy:     fetchPolicy,
		Fingerprint:     fmt.Sprintf("%x", fingerprintDigest),
	}, nil
}

func matchUpstreamSourceBillingProbeIdentityRows(rows *model.UpstreamSourceBillingProbeIdentityRows, fingerprint string) (upstreamSourceBillingProbeRequestIdentity, bool) {
	if rows == nil || fingerprint == "" {
		return upstreamSourceBillingProbeRequestIdentity{}, false
	}
	identity, identityErr := upstreamSourceBillingProbeIdentityFromRows(&rows.Probe, &rows.Source, &rows.Mapping, &rows.Channel)
	return identity, identityErr == nil && identity.Fingerprint == fingerprint
}

func (policy upstreamSourceBillingProbeFetchPolicy) validateURL(requestURL string) error {
	return common.ValidateURLWithFetchSetting(
		requestURL,
		policy.Enabled,
		policy.AllowPrivateIP,
		policy.DomainFilterMode,
		policy.IPFilterMode,
		policy.DomainList,
		policy.IPList,
		policy.AllowedPorts,
		policy.ApplyIPFilterForDomain,
	)
}

func (policy upstreamSourceBillingProbeFetchPolicy) protection() (*common.SSRFProtection, bool, error) {
	if !policy.Enabled {
		return nil, false, nil
	}
	protection, err := common.NewSSRFProtectionFromFetchSetting(
		policy.AllowPrivateIP,
		policy.DomainFilterMode,
		policy.IPFilterMode,
		policy.DomainList,
		policy.IPList,
		policy.AllowedPorts,
		policy.ApplyIPFilterForDomain,
	)
	return protection, true, err
}

func newUpstreamSourceBillingProbeHTTPClient(identity upstreamSourceBillingProbeRequestIdentity, timeout time.Duration) (*http.Client, error) {
	transport := newRelayHTTPTransport()
	if identity.ProxyURL != nil {
		if err := configureProxyTransport(transport, identity.ProxyURL); err != nil {
			return nil, &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorInvalidProxy}
		}
	} else {
		netDialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
		protectedDialer := &protectedFetchDialer{
			resolver:    net.DefaultResolver,
			dialContext: netDialer.DialContext,
			getProtection: func() (*common.SSRFProtection, bool, error) {
				return identity.FetchPolicy.protection()
			},
		}
		transport.Proxy = nil
		transport.DialContext = protectedDialer.DialContext
	}
	applyHTTPTransportPolicy(transport, identity.TransportPolicy)
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errUpstreamSourceBillingProbeRedirect
		},
	}, nil
}

func applyUpstreamSourceBillingProbeHeaderOverrides(header http.Header, raw string, bearerKey string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	invalid := func() error {
		return &upstreamSourceBillingProbeIdentityError{code: UpstreamSourceBillingProbeErrorIneligible}
	}
	if containsUpstreamSourceBillingProbeLiteralControl(bearerKey) {
		return invalid()
	}
	var overrides map[string]any
	if err := common.UnmarshalJsonStr(raw, &overrides); err != nil {
		return invalid()
	}
	for name, rawValue := range overrides {
		value, ok := rawValue.(string)
		if !ok || containsUpstreamSourceBillingProbeClientHeaderControl(value) {
			return invalid()
		}
		value = strings.ReplaceAll(value, "{api_key}", bearerKey)
		if !safeUpstreamSourceBillingProbeHeader(name, value) {
			return invalid()
		}
		header.Set(strings.TrimSpace(name), value)
	}
	return nil
}

func safeUpstreamSourceBillingProbeHeader(name string, value string) bool {
	name = strings.TrimSpace(name)
	if name == "" || containsUpstreamSourceBillingProbeLiteralControl(value) {
		return false
	}
	lowerName := strings.ToLower(name)
	if name == "*" || strings.HasPrefix(lowerName, "re:") || strings.HasPrefix(lowerName, "regex:") {
		return false
	}
	var normalizedName strings.Builder
	normalizedName.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			normalizedName.WriteByte(c)
			continue
		}
		if c >= 'A' && c <= 'Z' {
			normalizedName.WriteByte(c + ('a' - 'A'))
			continue
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			return false
		}
	}
	identityName := normalizedName.String()
	if identityName == "useragent" || identityName == "forwarded" || identityName == "via" ||
		identityName == "xrealip" || identityName == "cfconnectingip" ||
		strings.HasPrefix(identityName, "xforwarded") || strings.HasSuffix(identityName, "clientip") {
		return false
	}
	switch identityName {
	case "accept", "authorization", "connection", "contentlength", "cookie", "host", "keepalive",
		"acceptencoding", "proxyauthenticate", "proxyauthorization", "proxyconnection", "setcookie",
		"te", "trailer", "transferencoding", "upgrade":
		return false
	default:
		return true
	}
}

func containsUpstreamSourceBillingProbeClientHeaderControl(value string) bool {
	lowerValue := strings.ToLower(value)
	const prefix = "{client"
	const suffix = "header:"
	for searchStart := 0; searchStart < len(lowerValue); {
		relativeStart := strings.Index(lowerValue[searchStart:], prefix)
		if relativeStart < 0 {
			return false
		}
		separatorStart := searchStart + relativeStart + len(prefix)
		headerStart := separatorStart
		for headerStart < len(lowerValue) {
			character := lowerValue[headerStart]
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
				character == '{' || character == '}' {
				break
			}
			headerStart++
		}
		if strings.HasPrefix(lowerValue[headerStart:], suffix) {
			return true
		}
		searchStart += relativeStart + 1
	}
	return false
}

func containsUpstreamSourceBillingProbeLiteralControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}
