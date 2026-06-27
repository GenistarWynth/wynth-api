package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/go-redis/redis/v8"
)

// Redis-backed coordination for the account-pool runtime managers.
//
// These give multi-instance (horizontally scaled) deployments consistent
// behavior for the three correctness-critical signals named in the migration
// goal — concurrency (并发), affinity (亲和), and block (屏蔽) — so that a pin,
// a per-account/per-user concurrency limit, or a just-failed block established
// on one instance is honored by every other instance.
//
// Design contract for every manager below:
//   - When Redis is OFF (single-instance / Redis unavailable at boot), behavior
//     is byte-identical to the original in-memory managers.
//   - When Redis is ON, Redis is authoritative. If an individual Redis op
//     ERRORS at request time, the manager degrades to the in-memory path for
//     that op (fail-safe per-instance enforcement) rather than failing the
//     request. It never blocks a request because Redis hiccuped.
//   - Concurrency leases are self-healing: each lease is a ZSET member scored by
//     its expiry, so a crashed instance's held slots are reclaimed once their
//     TTL passes (a plain INCR counter would leak the slot forever).
//
// Selection recency is intentionally NOT moved to Redis: it is a per-instance
// load-distribution tie-breaker, not a correctness signal, and each instance
// distributing its own load is acceptable.

const (
	accountPoolRedisKeyPrefix = "account_pool:"
	accountPoolRedisOpTimeout = 2 * time.Second

	// accountPoolRedisAffinityTTLSeconds is the sliding idle TTL for a pinned
	// session, enforced by the Redis key expiry (mirrors the in-memory idle TTL).
	accountPoolRedisAffinityTTLSeconds = accountPoolRuntimeAffinityTTLSeconds
)

// accountPoolRedisLeaseTTLSeconds is the crash-recovery safety net for a
// distributed concurrency lease. Normal completion releases the slot via ZREM;
// this TTL only governs two things:
//   - how long a slot stays held after an instance dies mid-request without
//     releasing (lower = faster recovery), and
//   - the longest a single request may run before its slot is reclaimed while
//     still in flight, which would let a concurrent request transiently exceed
//     the cap (higher = safer against over-subscription).
//
// The default (1800s/30min) comfortably exceeds virtually every real request so
// over-subscription effectively never happens, and 30min of slightly reduced
// concurrency on a surviving instance after a crash is far safer than briefly
// exceeding an upstream's per-account concurrency limit (which risks 429s/bans).
// Operators who need faster crash recovery can lower it via the env var.
var accountPoolRedisLeaseTTLSeconds = int64(common.GetEnvOrDefault("ACCOUNT_POOL_REDIS_LEASE_TTL_SECONDS", 1800))

func accountPoolRedisOn() bool {
	return common.RedisEnabled && common.RDB != nil
}

func accountPoolRedisCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), accountPoolRedisOpTimeout)
}

func accountPoolBlockKey(accountID int) string {
	return accountPoolRedisKeyPrefix + "block:" + strconv.Itoa(accountID)
}

func accountPoolLeaseKey(accountID int) string {
	return accountPoolRedisKeyPrefix + "lease:" + strconv.Itoa(accountID)
}

func accountPoolUserConcurrencyRedisKey(bindingID, userID int) string {
	return accountPoolRedisKeyPrefix + "uconc:" + strconv.Itoa(bindingID) + ":" + strconv.Itoa(userID)
}

func accountPoolAffinityRedisKey(key string) string {
	return accountPoolRedisKeyPrefix + "affinity:" + key
}

// ---- Block (屏蔽) ----------------------------------------------------------

// accountPoolBlockSetScript stores blockedUntil with monotonic max-semantics
// (only widens the window) and a TTL that matches the remaining block duration,
// so the key disappears on its own once the block lapses.
var accountPoolBlockSetScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
local newv = tonumber(ARGV[1])
local now = tonumber(ARGV[2])
if cur and tonumber(cur) >= newv then return 0 end
local ttl = newv - now
if ttl < 1 then ttl = 1 end
redis.call('SET', KEYS[1], newv, 'EX', ttl)
return 1
`)

func accountPoolRedisBlockSet(accountID int, until int64) error {
	ctx, cancel := accountPoolRedisCtx()
	defer cancel()
	now := common.GetTimestamp()
	return accountPoolBlockSetScript.Run(ctx, common.RDB,
		[]string{accountPoolBlockKey(accountID)}, until, now).Err()
}

func accountPoolRedisBlocked(accountID int, now int64) (bool, error) {
	ctx, cancel := accountPoolRedisCtx()
	defer cancel()
	v, err := common.RDB.Get(ctx, accountPoolBlockKey(accountID)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	until, perr := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if perr != nil {
		return false, nil
	}
	return now < until, nil
}

func accountPoolRedisBlockClear(accountID int) {
	ctx, cancel := accountPoolRedisCtx()
	defer cancel()
	_ = common.RDB.Del(ctx, accountPoolBlockKey(accountID)).Err()
}

// ---- Affinity (亲和) -------------------------------------------------------

// accountPoolAffinityRememberScript stores "bindingID:accountID:createdAt" with
// a sliding idle TTL. createdAt is preserved across refreshes so the absolute
// hard cap stays anchored to the first pin (mirrors the in-memory semantics).
var accountPoolAffinityRememberScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
local createdAt = ARGV[3]
if cur then
  local c = string.match(cur, '^%d+:%d+:(%-?%d+)$')
  if c then createdAt = c end
end
redis.call('SET', KEYS[1], ARGV[1] .. ':' .. ARGV[2] .. ':' .. createdAt, 'EX', ARGV[4])
return tonumber(createdAt)
`)

func accountPoolRedisAffinityRemember(key string, bindingID, accountID int, now int64) error {
	ctx, cancel := accountPoolRedisCtx()
	defer cancel()
	return accountPoolAffinityRememberScript.Run(ctx, common.RDB,
		[]string{accountPoolAffinityRedisKey(key)},
		bindingID, accountID, now, accountPoolRedisAffinityTTLSeconds).Err()
}

// accountPoolRedisAffinityLookup is read-only except for eviction: it does NOT
// refresh the sliding idle TTL (only remember() does, matching the in-memory
// manager), so a session that only reads without re-pinning eventually expires.
func accountPoolRedisAffinityLookup(key string, bindingID int, now int64) (int, bool, error) {
	ctx, cancel := accountPoolRedisCtx()
	defer cancel()
	rkey := accountPoolAffinityRedisKey(key)
	v, err := common.RDB.Get(ctx, rkey).Result()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 3 {
		return 0, false, nil
	}
	storedBinding, _ := strconv.Atoi(parts[0])
	storedAccount, _ := strconv.Atoi(parts[1])
	createdAt, _ := strconv.ParseInt(parts[2], 10, 64)
	if storedBinding != bindingID || storedAccount <= 0 {
		accountPoolRedisAffinityForget(key)
		return 0, false, nil
	}
	// Hard cap: evict pins older than the absolute lifetime even if the sliding
	// idle TTL keeps getting refreshed.
	if now >= createdAt+accountPoolRuntimeAffinityHardCapSeconds {
		accountPoolRedisAffinityForget(key)
		return 0, false, nil
	}
	return storedAccount, true, nil
}

func accountPoolRedisAffinityForget(key string) {
	ctx, cancel := accountPoolRedisCtx()
	defer cancel()
	_ = common.RDB.Del(ctx, accountPoolAffinityRedisKey(key)).Err()
}

// ---- Concurrency leases (并发) ---------------------------------------------

// accountPoolLeaseAcquireScript implements a self-healing fixed-window
// concurrency limiter on a ZSET: purge expired members, count the live ones,
// and add ours iff we are under the cap. Each member is a unique token scored by
// its expiry so a crashed holder's slot is reclaimed once its score passes.
var accountPoolLeaseAcquireScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local max = tonumber(ARGV[2])
local expiry = tonumber(ARGV[3])
local token = ARGV[4]
local ttl = tonumber(ARGV[5])
redis.call('ZREMRANGEBYSCORE', key, '-inf', now)
local count = redis.call('ZCARD', key)
if count >= max then return 0 end
redis.call('ZADD', key, expiry, token)
redis.call('EXPIRE', key, ttl)
return 1
`)

// accountPoolRedisAcquireLease attempts to claim one of maxConcurrency slots for
// the given key. On success it returns an idempotent release closure that frees
// the slot (ZREM). On a denied slot it returns (nil,false,nil). On a Redis error
// it returns (nil,false,err) so the caller can fall back to the in-memory path.
func accountPoolRedisAcquireLease(key string, maxConcurrency int) (accountPoolRuntimeReleaseFunc, bool, error) {
	ctx, cancel := accountPoolRedisCtx()
	defer cancel()
	now := common.GetTimestamp()
	token := common.GetUUID()
	expiry := now + accountPoolRedisLeaseTTLSeconds
	res, err := accountPoolLeaseAcquireScript.Run(ctx, common.RDB,
		[]string{key}, now, maxConcurrency, expiry, token, accountPoolRedisLeaseTTLSeconds).Int64()
	if err != nil {
		return nil, false, err
	}
	if res != 1 {
		return nil, false, nil
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			c, cc := accountPoolRedisCtx()
			defer cc()
			_ = common.RDB.ZRem(c, key, token).Err()
		})
	}, true, nil
}
