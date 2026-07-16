package common

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deleteRedisKeyBeforeWriteHook struct {
	server     *miniredis.Miniredis
	key        string
	deleteOnce sync.Once
}

func (h *deleteRedisKeyBeforeWriteHook) deleteKey() {
	h.deleteOnce.Do(func() {
		h.server.Del(h.key)
	})
}

func (h *deleteRedisKeyBeforeWriteHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	switch cmd.Name() {
	case "eval", "evalsha":
		h.deleteKey()
	}
	return ctx, nil
}

func (h *deleteRedisKeyBeforeWriteHook) AfterProcess(context.Context, redis.Cmder) error {
	return nil
}

func (h *deleteRedisKeyBeforeWriteHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	h.deleteKey()
	return ctx, nil
}

func (h *deleteRedisKeyBeforeWriteHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func TestRedisHSetFieldDoesNotRecreateKeyDeletedBeforeWrite(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousRDB := RDB
	RDB = client
	t.Cleanup(func() {
		require.NoError(t, client.Close())
		RDB = previousRDB
		server.Close()
	})

	ctx := context.Background()
	const key = "redis-hset-field:deleted-before-write"
	require.NoError(t, client.HSet(ctx, key, "existing", "value").Err())
	require.NoError(t, client.Expire(ctx, key, time.Minute).Err())

	client.AddHook(&deleteRedisKeyBeforeWriteHook{server: server, key: key})

	require.NoError(t, RedisHSetField(key, "updated", "value"))
	assert.False(t, server.Exists(key))
}
