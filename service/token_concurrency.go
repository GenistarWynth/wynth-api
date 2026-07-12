package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

const tokenConcurrencyKeyPrefix = "token_concurrency:lease:"

var tokenConcurrencyLeaseTTL = 30 * time.Minute

func tokenConcurrencyKey(tokenID int) string {
	return tokenConcurrencyKeyPrefix + strconv.Itoa(tokenID)
}

func AcquireTokenConcurrencyLease(ctx context.Context, tokenID int) func() {
	noop := func() {}
	if tokenID <= 0 || !common.RedisEnabled || common.RDB == nil {
		return noop
	}
	memberBytes := make([]byte, 16)
	if _, err := rand.Read(memberBytes); err != nil {
		return noop
	}
	member := hex.EncodeToString(memberBytes)
	now := time.Now()
	pipe := common.RDB.TxPipeline()
	pipe.ZRemRangeByScore(ctx, tokenConcurrencyKey(tokenID), "-inf", strconv.FormatInt(now.UnixMilli(), 10))
	pipe.ZAdd(ctx, tokenConcurrencyKey(tokenID), &redis.Z{Score: float64(now.Add(tokenConcurrencyLeaseTTL).UnixMilli()), Member: member})
	pipe.Expire(ctx, tokenConcurrencyKey(tokenID), tokenConcurrencyLeaseTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return noop
	}
	var once sync.Once
	return func() {
		once.Do(func() { _ = common.RDB.ZRem(context.Background(), tokenConcurrencyKey(tokenID), member).Err() })
	}
}

func GetTokenConcurrencyCounts(ctx context.Context, tokenIDs []int) map[int]int {
	counts := make(map[int]int, len(tokenIDs))
	if len(tokenIDs) == 0 {
		return counts
	}
	for _, id := range tokenIDs {
		counts[id] = 0
	}
	if !common.RedisEnabled || common.RDB == nil {
		return counts
	}
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	pipe := common.RDB.Pipeline()
	commands := make(map[int]*redis.IntCmd, len(tokenIDs))
	for _, id := range tokenIDs {
		if id <= 0 {
			continue
		}
		key := tokenConcurrencyKey(id)
		pipe.ZRemRangeByScore(ctx, key, "-inf", now)
		commands[id] = pipe.ZCard(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return counts
	}
	for id, cmd := range commands {
		if n, err := cmd.Result(); err == nil {
			counts[id] = int(n)
		}
	}
	return counts
}
