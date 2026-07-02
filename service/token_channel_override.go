package service

import (
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
)

const (
	tokenChannelOverrideNamespace = "new-api:token_channel_override:v1"

	tokenChannelOverrideDefaultTTLSeconds = 1800  // 30 minutes
	tokenChannelOverrideMaxTTLSeconds     = 86400 // 24 hours
	tokenChannelOverrideMaxEntries        = 100_000
)

// TokenChannelOverride is an admin-configured, temporary, per-token forced channel.
// It carries ONLY the channel object to force; the token's real group (and therefore
// billing/logging attribution) is never stored here — it is resolved per request in the
// distributor, so an override can never corrupt group accounting.
type TokenChannelOverride struct {
	ChannelId   int   `json:"channel_id"`
	SetByUserId int   `json:"set_by_user_id"`
	CreatedAt   int64 `json:"created_at"`
}

var (
	tokenChannelOverrideCacheOnce sync.Once
	tokenChannelOverrideCache     *cachex.HybridCache[TokenChannelOverride]
)

// getTokenChannelOverrideCache returns the process-wide override cache: Redis when
// enabled, otherwise an in-memory LRU with TTL. Mirrors the channel-affinity cache
// singleton (service/channel_affinity.go) so behavior is identical (no DB, TTL-backed).
func getTokenChannelOverrideCache() *cachex.HybridCache[TokenChannelOverride] {
	tokenChannelOverrideCacheOnce.Do(func() {
		tokenChannelOverrideCache = cachex.NewHybridCache[TokenChannelOverride](cachex.HybridCacheConfig[TokenChannelOverride]{
			Namespace: cachex.Namespace(tokenChannelOverrideNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[TokenChannelOverride]{},
			Memory: func() *hot.HotCache[string, TokenChannelOverride] {
				return hot.NewHotCache[string, TokenChannelOverride](hot.LRU, tokenChannelOverrideMaxEntries).
					WithTTL(time.Duration(tokenChannelOverrideDefaultTTLSeconds) * time.Second).
					WithJanitor().
					Build()
			},
		})
	})
	return tokenChannelOverrideCache
}

// ClampTokenChannelOverrideTTL bounds an admin-supplied TTL in seconds:
// <=0 -> default (30m); > max -> max (24h); otherwise the value itself.
func ClampTokenChannelOverrideTTL(seconds int) time.Duration {
	if seconds <= 0 {
		return tokenChannelOverrideDefaultTTLSeconds * time.Second
	}
	if seconds > tokenChannelOverrideMaxTTLSeconds {
		return tokenChannelOverrideMaxTTLSeconds * time.Second
	}
	return time.Duration(seconds) * time.Second
}

func tokenChannelOverrideKey(tokenId int) string {
	return strconv.Itoa(tokenId)
}

// SetTokenChannelOverride stores (or replaces) a forced channel for a token with the given TTL.
func SetTokenChannelOverride(tokenId, channelId, setByUserId int, ttl time.Duration) error {
	return getTokenChannelOverrideCache().SetWithTTL(tokenChannelOverrideKey(tokenId), TokenChannelOverride{
		ChannelId:   channelId,
		SetByUserId: setByUserId,
		CreatedAt:   time.Now().Unix(),
	}, ttl)
}

// GetTokenChannelOverride returns the cached override for a token, if present and unexpired.
func GetTokenChannelOverride(tokenId int) (TokenChannelOverride, bool) {
	ov, found, err := getTokenChannelOverrideCache().Get(tokenChannelOverrideKey(tokenId))
	if err != nil || !found {
		return TokenChannelOverride{}, false
	}
	return ov, true
}

// ClearTokenChannelOverride removes any override for a token. Idempotent.
func ClearTokenChannelOverride(tokenId int) error {
	_, err := getTokenChannelOverrideCache().DeleteMany([]string{tokenChannelOverrideKey(tokenId)})
	return err
}
