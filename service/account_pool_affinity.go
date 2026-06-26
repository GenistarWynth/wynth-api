package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const accountPoolRuntimeAffinityTTLSeconds = int64(30 * 60)

// accountPoolRuntimeAffinityHardCapSeconds is the maximum absolute lifetime of a pinned entry.
// Even if the session keeps refreshing the sliding idle TTL, the pin is evicted after 4h so
// that admin rebalancing and newly-added accounts can take effect on active sessions.
const accountPoolRuntimeAffinityHardCapSeconds = int64(4 * 60 * 60)

type accountPoolRuntimeAffinityEntry struct {
	bindingID int
	accountID int
	createdAt int64 // wall time when the entry was first created; never updated on refresh
	expiresAt int64 // sliding idle expiry; refreshed on every remember call
}

type accountPoolRuntimeAffinityManager struct {
	mu      sync.Mutex
	entries map[string]accountPoolRuntimeAffinityEntry
}

var accountPoolRuntimeAffinities = newAccountPoolRuntimeAffinityManager()

func newAccountPoolRuntimeAffinityManager() *accountPoolRuntimeAffinityManager {
	return &accountPoolRuntimeAffinityManager{entries: map[string]accountPoolRuntimeAffinityEntry{}}
}

func BuildAccountPoolRuntimeAffinityKey(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) string {
	signal := accountPoolRuntimeAffinitySignal(c, info, request)
	if signal == "" || info == nil || info.ChannelMeta == nil || info.ChannelId <= 0 {
		return ""
	}
	raw := fmt.Sprintf(
		"v1|channel:%d|origin:%s|upstream:%s|user:%d|token:%d|signal:%s",
		info.ChannelId,
		strings.TrimSpace(info.OriginModelName),
		strings.TrimSpace(info.UpstreamModelName),
		info.UserId,
		info.TokenId,
		signal,
	)
	return accountPoolRuntimeAffinityDigest(raw)
}

func accountPoolRuntimeAffinitySignal(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) string {
	if headerValue := accountPoolRuntimeAffinityHeaderSignal(c, info); headerValue != "" {
		return "header:" + headerValue
	}
	switch req := request.(type) {
	case *dto.OpenAIResponsesRequest:
		if value := strings.TrimSpace(req.PreviousResponseID); value != "" {
			return "responses_previous:" + value
		}
		if value := strings.TrimSpace(string(req.Conversation)); value != "" && value != "null" {
			return "responses_conversation:" + accountPoolRuntimeAffinityDigest(value)
		}
	case *dto.OpenAIResponsesCompactionRequest:
		if value := strings.TrimSpace(req.PreviousResponseID); value != "" {
			return "responses_compaction_previous:" + value
		}
	}
	return ""
}

func accountPoolRuntimeAffinityHeaderSignal(c *gin.Context, info *relaycommon.RelayInfo) string {
	for _, name := range []string{"Session_id", "session_id", "X-Session-Id", "X-Conversation-Id", "OpenAI-Conversation-Id"} {
		if value := strings.TrimSpace(accountPoolRuntimeHeaderValue(c, info, name)); value != "" {
			return name + ":" + value
		}
	}
	return ""
}

func accountPoolRuntimeHeaderValue(c *gin.Context, info *relaycommon.RelayInfo, name string) string {
	if c != nil && c.Request != nil {
		if value := c.Request.Header.Get(name); value != "" {
			return value
		}
	}
	if info != nil && len(info.RequestHeaders) > 0 {
		if value := info.RequestHeaders[name]; value != "" {
			return value
		}
		header := http.Header{}
		for key, value := range info.RequestHeaders {
			header.Set(key, value)
		}
		return header.Get(name)
	}
	return ""
}

func accountPoolRuntimeAffinityDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func rememberAccountPoolRuntimeAffinity(key string, bindingID int, accountID int, now int64) {
	accountPoolRuntimeAffinities.remember(key, bindingID, accountID, now)
}

func lookupAccountPoolRuntimeAffinity(key string, bindingID int, now int64) (int, bool) {
	return accountPoolRuntimeAffinities.lookup(key, bindingID, now)
}

func forgetAccountPoolRuntimeAffinity(key string) {
	accountPoolRuntimeAffinities.forget(key)
}

func (m *accountPoolRuntimeAffinityManager) remember(key string, bindingID int, accountID int, now int64) {
	if key == "" || bindingID <= 0 || accountID <= 0 {
		return
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	createdAt := now
	if existing, ok := m.entries[key]; ok {
		// Preserve the original birth time so the hard cap is anchored to when the session
		// was first pinned, not to the most recent refresh.
		createdAt = existing.createdAt
	}
	m.entries[key] = accountPoolRuntimeAffinityEntry{
		bindingID: bindingID,
		accountID: accountID,
		createdAt: createdAt,
		expiresAt: now + accountPoolRuntimeAffinityTTLSeconds,
	}
}

func (m *accountPoolRuntimeAffinityManager) lookup(key string, bindingID int, now int64) (int, bool) {
	if key == "" || bindingID <= 0 {
		return 0, false
	}
	if now <= 0 {
		now = common.GetTimestamp()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[key]
	if !ok {
		return 0, false
	}
	if entry.expiresAt <= now || entry.bindingID != bindingID || entry.accountID <= 0 {
		delete(m.entries, key)
		return 0, false
	}
	// Hard cap: evict entries that have been alive longer than the absolute lifetime limit,
	// even if the sliding idle TTL was recently refreshed by a remember() call.
	if now >= entry.createdAt+accountPoolRuntimeAffinityHardCapSeconds {
		delete(m.entries, key)
		return 0, false
	}
	return entry.accountID, true
}

func (m *accountPoolRuntimeAffinityManager) forget(key string) {
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
}

func resetAccountPoolRuntimeAffinitiesForTest() {
	accountPoolRuntimeAffinities = newAccountPoolRuntimeAffinityManager()
}
