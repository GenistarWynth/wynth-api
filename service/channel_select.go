package service

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

type RetryParam struct {
	Ctx          *gin.Context
	TokenGroup   string
	ModelName    string
	RequestPath  string
	Retry        *int
	resetNextTry bool
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) SetRetry(retry int) {
	p.Retry = &retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.resetNextTry {
		p.resetNextTry = false
		return
	}
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

func (p *RetryParam) ResetRetryNextTry() {
	p.resetNextTry = true
}

func attemptedChannelIDSetFromContext(ctx *gin.Context) map[int]struct{} {
	if ctx == nil {
		return nil
	}
	useChannels := ctx.GetStringSlice("use_channel")
	if len(useChannels) == 0 {
		return nil
	}
	attempted := make(map[int]struct{}, len(useChannels))
	for _, rawID := range useChannels {
		id, err := strconv.Atoi(strings.TrimSpace(rawID))
		if err != nil || id <= 0 {
			continue
		}
		attempted[id] = struct{}{}
	}
	if len(attempted) == 0 {
		return nil
	}
	return attempted
}

// CacheGetRandomSatisfiedChannel tries to get a random channel that satisfies the requirements.
// 尝试获取一个满足要求的随机渠道。
//
// It reads request-local attempted channel IDs from ctx.GetStringSlice("use_channel")
// on every call and passes them to model selection so retries keep strict priority
// among the highest-priority channels that have not already been tried.
// 每次调用都会从 ctx.GetStringSlice("use_channel") 读取本次请求已经尝试过的渠道 ID，
// 并传递给模型选择逻辑，使重试在未尝试渠道中保持严格优先级。
//
// For "auto" tokenGroup:
// 对于 "auto" tokenGroup：
//
//   - Starts from ContextKeyAutoGroupIndex when present.
//     使用 ContextKeyAutoGroupIndex 跟踪当前分组索引。
//
//   - Keeps selecting in the current auto group until all matching channels in
//     that group have been attempted or otherwise filtered out.
//     当前自动分组中还有符合条件的未尝试渠道时，会继续选择当前分组。
//
//   - Advances to the next auto group only when GetRandomSatisfiedChannel returns
//     nil for the current group, meaning the group is exhausted for this request.
//     仅当当前分组返回 nil（本次请求已耗尽）时，才切换到下一个自动分组。
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	var channel *model.Channel
	var err error
	selectGroup := param.TokenGroup
	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)
	attemptedChannelIDs := attemptedChannelIDSetFromContext(param.Ctx)

	if param.TokenGroup == "auto" {
		if len(setting.GetAutoGroups()) == 0 {
			return nil, selectGroup, errors.New("auto groups is not enabled")
		}
		autoGroups := GetUserAutoGroup(userGroup)

		// startGroupIndex: the group index to start searching from
		// startGroupIndex: 开始搜索的分组索引
		startGroupIndex := 0

		if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if idx, ok := lastGroupIndex.(int); ok {
				startGroupIndex = idx
			}
		}

		for i := startGroupIndex; i < len(autoGroups); i++ {
			autoGroup := autoGroups[i]
			logger.LogDebug(param.Ctx, "Auto selecting group: %s, retry: %d", autoGroup, param.GetRetry())

			channel, err = model.GetRandomSatisfiedChannel(autoGroup, param.ModelName, param.GetRetry(), param.RequestPath, attemptedChannelIDs)
			if err != nil {
				return nil, autoGroup, err
			}
			if channel == nil {
				// Current group has no available channel for this model, try next group
				// 当前分组没有该模型的可用渠道，尝试下一个分组
				logger.LogDebug(param.Ctx, "No available channel in group %s for model %s, trying next group", autoGroup, param.ModelName)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				continue
			}
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
			selectGroup = autoGroup
			logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)
			break
		}
	} else {
		channel, err = model.GetRandomSatisfiedChannel(param.TokenGroup, param.ModelName, param.GetRetry(), param.RequestPath, attemptedChannelIDs)
		if err != nil {
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}
