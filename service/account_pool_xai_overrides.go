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

	"golang.org/x/net/http/httpguts"

	"github.com/QuantumNous/new-api/model"
)

const (
	accountPoolXAIUnsafeBaseURLEnv         = "ACCOUNT_POOL_XAI_ALLOW_UNSAFE_BASE_URL"
	accountPoolXAIHeaderOverrideMaxEntries = 16
	accountPoolXAIHeaderOverrideMaxName    = 128
	accountPoolXAIHeaderOverrideMaxValue   = 4096
	accountPoolXAIHeaderOverrideMaxTotal   = 16 * 1024
)

type accountPoolXAIHostResolver func(context.Context, string) ([]net.IP, error)

func normalizeAccountPoolXAIOverrides(ctx context.Context, platform string, credential AccountPoolCredentialConfig) (AccountPoolCredentialConfig, error) {
	hasOverrides := credential.BaseURL != nil || credential.HeaderOverrideEnabled != nil || credential.HeaderOverrides != nil
	if !hasOverrides {
		return credential, nil
	}
	if platform != model.AccountPoolPlatformXAI {
		return credential, errors.New("account base URL and header overrides are supported only for xai pools")
	}
	if credential.BaseURL != nil {
		normalized, err := normalizeAccountPoolXAIBaseURL(ctx, *credential.BaseURL, accountPoolXAIUnsafeBaseURLAllowed(), nil)
		if err != nil {
			return credential, err
		}
		credential.BaseURL = &normalized
	}
	if credential.HeaderOverrides != nil {
		normalized, err := normalizeAccountPoolXAIHeaderOverrides(credential.HeaderOverrides)
		if err != nil {
			return credential, err
		}
		credential.HeaderOverrides = normalized
	}
	return credential, nil
}

func accountPoolXAIUnsafeBaseURLAllowed() bool {
	allowed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(accountPoolXAIUnsafeBaseURLEnv)))
	return err == nil && allowed
}

func normalizeAccountPoolXAIBaseURL(
	ctx context.Context,
	raw string,
	allowUnsafe bool,
	resolver accountPoolXAIHostResolver,
) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() == false || strings.TrimSpace(parsed.Hostname()) == "" {
		return "", errors.New("xai account base URL is invalid")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("xai account base URL must not contain credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(allowUnsafe && parsed.Scheme == "http") {
		return "", errors.New("xai account base URL must use HTTPS")
	}
	if !allowUnsafe {
		hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if hostname != "x.ai" && !strings.HasSuffix(hostname, ".x.ai") {
			if ip := net.ParseIP(hostname); ip != nil {
				if accountPoolXAIPrivateIP(ip) {
					return "", errors.New("xai account base URL resolves to a private address")
				}
			} else {
				if resolver == nil {
					resolver = func(ctx context.Context, host string) ([]net.IP, error) {
						return net.DefaultResolver.LookupIP(ctx, "ip", host)
					}
				}
				if ctx == nil {
					ctx = context.Background()
				}
				addresses, resolveErr := resolver(ctx, hostname)
				if resolveErr != nil || len(addresses) == 0 {
					return "", errors.New("xai account base URL host cannot be resolved")
				}
				for _, address := range addresses {
					if accountPoolXAIPrivateIP(address) {
						return "", errors.New("xai account base URL resolves to a private address")
					}
				}
			}
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func accountPoolXAIPrivateIP(ip net.IP) bool {
	return ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast()
}

func normalizeAccountPoolXAIHeaderOverrides(headers map[string]string) (map[string]string, error) {
	if headers == nil {
		return nil, nil
	}
	if len(headers) > accountPoolXAIHeaderOverrideMaxEntries {
		return nil, fmt.Errorf("xai account header overrides exceed %d entries", accountPoolXAIHeaderOverrideMaxEntries)
	}
	normalized := make(map[string]string, len(headers))
	totalSize := 0
	for rawName, rawValue := range headers {
		name := http.CanonicalHeaderKey(strings.TrimSpace(rawName))
		value := strings.TrimSpace(rawValue)
		if name == "" || len(name) > accountPoolXAIHeaderOverrideMaxName || !httpguts.ValidHeaderFieldName(name) {
			return nil, errors.New("xai account header override contains an invalid name")
		}
		if value == "" || len(value) > accountPoolXAIHeaderOverrideMaxValue || !httpguts.ValidHeaderFieldValue(value) {
			return nil, fmt.Errorf("xai account header override %s contains an invalid value", name)
		}
		if accountPoolXAIDangerousHeader(name) {
			return nil, fmt.Errorf("xai account header override %s is not allowed", name)
		}
		if _, exists := normalized[name]; exists {
			return nil, fmt.Errorf("xai account header override %s is duplicated", name)
		}
		totalSize += len(name) + len(value)
		if totalSize > accountPoolXAIHeaderOverrideMaxTotal {
			return nil, errors.New("xai account header overrides are too large")
		}
		normalized[name] = value
	}
	return normalized, nil
}

func accountPoolXAIDangerousHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(name, "proxy-") {
		return true
	}
	switch name {
	case "host", "content-length", "authorization", "cookie", "set-cookie", "x-api-key",
		"connection", "transfer-encoding", "upgrade", "keep-alive", "te", "trailer":
		return true
	default:
		return false
	}
}
