package controller

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// ForceChannelRequest is the body of the admin force-channel endpoint. TTLSeconds is
// optional: <=0 falls back to the default TTL and values above the max are clamped
// (see service.ClampTokenChannelOverrideTTL).
type ForceChannelRequest struct {
	ChannelId  int `json:"channel_id"`
	TTLSeconds int `json:"ttl_seconds"`
}

// AdminForceTokenChannel pins a target user's token to a specific same-group channel at
// runtime. The override is cache-backed and TTL'd; subsequent requests for that token are
// routed to the channel, while any in-flight request is left untouched. Set-time validation
// is deliberately coarse (channel exists + enabled + belongs to the token's effective group);
// the authoritative per-request gate (model support, request path, live channel status) lives
// in the distributor and fails gracefully rather than aborting live traffic.
func AdminForceTokenChannel(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	tid, err := strconv.Atoi(c.Param("tid"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var req ForceChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	// Scopes the token to the target user (rejects a tid the user does not own).
	token, err := model.GetTokenByIds(tid, targetUser.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	channel, err := model.CacheGetChannel(req.ChannelId)
	if err != nil || channel == nil {
		common.ApiError(c, fmt.Errorf("channel %d not found", req.ChannelId))
		return
	}
	if channel.Status != common.ChannelStatusEnabled {
		common.ApiError(c, fmt.Errorf("channel %d is disabled", req.ChannelId))
		return
	}

	// Effective group mirrors middleware/auth.go: the token's group overrides the user's.
	// For an "auto" token the channel must belong to one of the user's auto sub-groups.
	effectiveGroup := targetUser.Group
	if token.Group != "" {
		effectiveGroup = token.Group
	}
	channelGroups := channel.GetGroups()
	inGroup := false
	if effectiveGroup == "auto" {
		for _, g := range service.GetUserAutoGroup(targetUser.Group) {
			if slices.Contains(channelGroups, g) {
				inGroup = true
				break
			}
		}
	} else {
		inGroup = slices.Contains(channelGroups, effectiveGroup)
	}
	if !inGroup {
		common.ApiError(c, fmt.Errorf("channel %d is not in the token's group %q", req.ChannelId, effectiveGroup))
		return
	}

	if err := service.SetTokenChannelOverride(tid, req.ChannelId, c.GetInt("id"), service.ClampTokenChannelOverrideTTL(req.TTLSeconds)); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, targetUser.Id, "user.token.force_channel", map[string]interface{}{
		"token_id": tid, "channel_id": req.ChannelId, "ttl_seconds": req.TTLSeconds,
	})
	common.ApiSuccess(c, gin.H{"token_id": tid, "channel_id": req.ChannelId})
}

// AdminClearTokenForceChannel removes any runtime channel override for a target user's token.
// Idempotent: clearing a token with no override succeeds.
func AdminClearTokenForceChannel(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	tid, err := strconv.Atoi(c.Param("tid"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if _, err := model.GetTokenByIds(tid, targetUser.Id); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := service.ClearTokenChannelOverride(tid); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, targetUser.Id, "user.token.clear_force_channel", map[string]interface{}{"token_id": tid})
	common.ApiSuccess(c, gin.H{"token_id": tid})
}

// AdminGetTokenForceChannel reports the current runtime channel override for a target
// user's token (for the admin UI drawer). active=false means no override is in effect.
func AdminGetTokenForceChannel(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	tid, err := strconv.Atoi(c.Param("tid"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if _, err := model.GetTokenByIds(tid, targetUser.Id); err != nil {
		common.ApiError(c, err)
		return
	}
	ov, active := service.GetTokenChannelOverride(tid)
	common.ApiSuccess(c, gin.H{
		"active":         active,
		"channel_id":     ov.ChannelId,
		"set_by_user_id": ov.SetByUserId,
		"created_at":     ov.CreatedAt,
	})
}
