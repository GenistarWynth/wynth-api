package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"

	"github.com/QuantumNous/new-api/model"
)

const (
	accountPoolOutboundUnsafeBaseURLEnv         = "ACCOUNT_POOL_OUTBOUND_ALLOW_UNSAFE_BASE_URL"
	accountPoolXAIUnsafeBaseURLEnv              = "ACCOUNT_POOL_XAI_ALLOW_UNSAFE_BASE_URL"
	accountPoolOutboundHeaderOverrideMaxEntries = 16
	accountPoolOutboundHeaderOverrideMaxName    = 128
	accountPoolOutboundHeaderOverrideMaxValue   = 4096
	accountPoolOutboundHeaderOverrideMaxTotal   = 16 * 1024
	accountPoolOutboundDNSLookupTimeout         = 3 * time.Second
)

type accountPoolOutboundHostResolver func(context.Context, string) ([]net.IP, error)

func normalizeAccountPoolOutboundOverrides(
	ctx context.Context,
	platform string,
	credential AccountPoolCredentialConfig,
	resolver accountPoolOutboundHostResolver,
) (AccountPoolCredentialConfig, error) {
	hasOverrides := credential.BaseURL != nil || credential.HeaderOverrideEnabled != nil || credential.HeaderOverrides != nil
	if !hasOverrides {
		return credential, nil
	}
	if !accountPoolOutboundOverridesSupported(platform) {
		return credential, fmt.Errorf("account outbound overrides are not supported for %s pools", platform)
	}
	if credential.BaseURL != nil {
		normalized, err := normalizeAccountPoolOutboundBaseURL(
			ctx,
			platform,
			*credential.BaseURL,
			accountPoolOutboundUnsafeBaseURLAllowed(platform),
			resolver,
		)
		if err != nil {
			return credential, err
		}
		credential.BaseURL = &normalized
	}
	if credential.HeaderOverrides != nil {
		normalized, err := normalizeAccountPoolOutboundHeaderOverrides(credential.HeaderOverrides)
		if err != nil {
			return credential, err
		}
		credential.HeaderOverrides = normalized
	}
	return credential, nil
}

func accountPoolOutboundOverridesSupported(platform string) bool {
	switch platform {
	case model.AccountPoolPlatformOpenAI,
		model.AccountPoolPlatformAnthropic,
		model.AccountPoolPlatformGemini,
		model.AccountPoolPlatformXAI:
		return true
	default:
		return false
	}
}

func accountPoolOutboundUnsafeBaseURLAllowed(platform string) bool {
	allowed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(accountPoolOutboundUnsafeBaseURLEnv)))
	if err == nil && allowed {
		return true
	}
	if platform != model.AccountPoolPlatformXAI {
		return false
	}
	allowed, err = strconv.ParseBool(strings.TrimSpace(os.Getenv(accountPoolXAIUnsafeBaseURLEnv)))
	return err == nil && allowed
}

func normalizeAccountPoolOutboundBaseURL(
	ctx context.Context,
	platform string,
	raw string,
	allowUnsafe bool,
	resolver accountPoolOutboundHostResolver,
) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || strings.TrimSpace(parsed.Hostname()) == "" {
		return "", errors.New("account outbound base URL is invalid")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("account outbound base URL must not contain credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(allowUnsafe && parsed.Scheme == "http") {
		return "", errors.New("account outbound base URL must use HTTPS")
	}
	if !allowUnsafe {
		hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if !accountPoolOutboundTrustedHost(platform, hostname) {
			if ip := net.ParseIP(hostname); ip != nil {
				if accountPoolOutboundPrivateIP(ip) {
					return "", errors.New("account outbound base URL resolves to a private address")
				}
			} else {
				if ctx == nil {
					ctx = context.Background()
				}
				if resolver == nil {
					lookupContext, cancel := context.WithTimeout(ctx, accountPoolOutboundDNSLookupTimeout)
					defer cancel()
					ctx = lookupContext
					resolver = func(ctx context.Context, host string) ([]net.IP, error) {
						return net.DefaultResolver.LookupIP(ctx, "ip", host)
					}
				}
				addresses, resolveErr := resolver(ctx, hostname)
				if resolveErr != nil || len(addresses) == 0 {
					return "", errors.New("account outbound base URL host cannot be resolved")
				}
				for _, address := range addresses {
					if accountPoolOutboundPrivateIP(address) {
						return "", errors.New("account outbound base URL resolves to a private address")
					}
				}
			}
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func accountPoolOutboundTrustedHost(platform string, hostname string) bool {
	switch platform {
	case model.AccountPoolPlatformOpenAI:
		return hostname == "api.openai.com"
	case model.AccountPoolPlatformAnthropic:
		return hostname == "api.anthropic.com"
	case model.AccountPoolPlatformGemini:
		return hostname == "googleapis.com" || strings.HasSuffix(hostname, ".googleapis.com")
	case model.AccountPoolPlatformXAI:
		return hostname == "x.ai" || strings.HasSuffix(hostname, ".x.ai")
	default:
		return false
	}
}

func accountPoolOutboundPrivateIP(ip net.IP) bool {
	return ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast()
}

func normalizeAccountPoolOutboundHeaderOverrides(headers map[string]string) (map[string]string, error) {
	if headers == nil {
		return nil, nil
	}
	if len(headers) > accountPoolOutboundHeaderOverrideMaxEntries {
		return nil, fmt.Errorf("account header overrides exceed %d entries", accountPoolOutboundHeaderOverrideMaxEntries)
	}
	normalized := make(map[string]string, len(headers))
	totalSize := 0
	for rawName, rawValue := range headers {
		name := http.CanonicalHeaderKey(strings.TrimSpace(rawName))
		value := strings.TrimSpace(rawValue)
		if name == "" || len(name) > accountPoolOutboundHeaderOverrideMaxName || !httpguts.ValidHeaderFieldName(name) {
			return nil, errors.New("account header override contains an invalid name")
		}
		if value == "" || len(value) > accountPoolOutboundHeaderOverrideMaxValue || !httpguts.ValidHeaderFieldValue(value) {
			return nil, fmt.Errorf("account header override %s contains an invalid value", name)
		}
		if accountPoolOutboundDangerousHeader(name) {
			return nil, fmt.Errorf("account header override %s is not allowed", name)
		}
		if _, exists := normalized[name]; exists {
			return nil, fmt.Errorf("account header override %s is duplicated", name)
		}
		totalSize += len(name) + len(value)
		if totalSize > accountPoolOutboundHeaderOverrideMaxTotal {
			return nil, errors.New("account header overrides are too large")
		}
		normalized[name] = value
	}
	return normalized, nil
}

func accountPoolOutboundDangerousHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(name, "proxy-") || strings.HasPrefix(name, "sec-websocket-") ||
		strings.HasPrefix(name, "x-forwarded-") {
		return true
	}
	switch name {
	case "host", "content-length", "authorization", "cookie", "set-cookie", "x-api-key",
		"x-goog-api-key", "x-goog-user-project", "openai-organization", "openai-project",
		"forwarded", "x-real-ip", "connection", "transfer-encoding", "upgrade", "keep-alive",
		"te", "trailer", "accept-encoding":
		return true
	default:
		return false
	}
}

// Legacy xAI helpers remain as compatibility seams for focused tests and any
// out-of-tree integrations. New account-pool code uses the shared helpers above.
type accountPoolXAIHostResolver = accountPoolOutboundHostResolver

func normalizeAccountPoolXAIBaseURL(ctx context.Context, raw string, allowUnsafe bool, resolver accountPoolXAIHostResolver) (string, error) {
	return normalizeAccountPoolOutboundBaseURL(ctx, model.AccountPoolPlatformXAI, raw, allowUnsafe, resolver)
}

func normalizeAccountPoolXAIHeaderOverrides(headers map[string]string) (map[string]string, error) {
	return normalizeAccountPoolOutboundHeaderOverrides(headers)
}

func accountPoolXAIUnsafeBaseURLAllowed() bool {
	return accountPoolOutboundUnsafeBaseURLAllowed(model.AccountPoolPlatformXAI)
}
