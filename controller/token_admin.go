package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// resolveManageableTargetUser parses the :id route param, loads that user, and
// enforces the role hierarchy (canManageTargetRole). On any failure it writes
// the response and returns (nil, false). AdminAuth/RootAuth middleware already
// guarantees the actor is at least an admin; this adds the per-target check.
func resolveManageableTargetUser(c *gin.Context) (*model.User, bool) {
	targetUserId, err := strconv.Atoi(c.Param("id"))
	if err != nil || targetUserId == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return nil, false
	}
	targetUser, err := model.GetUserById(targetUserId, false)
	if err != nil {
		common.ApiError(c, err)
		return nil, false
	}
	if !canManageTargetRole(c.GetInt("role"), targetUser.Role) {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
		return nil, false
	}
	return targetUser, true
}

func AdminGetUserTokens(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	pageInfo := common.GetPageQuery(c)
	tokens, err := model.GetAllUserTokens(targetUser.Id, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, _ := model.CountUserTokens(targetUser.Id)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func AdminSearchUserTokens(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	keyword := c.Query("keyword")
	token := c.Query("token")
	pageInfo := common.GetPageQuery(c)
	tokens, total, err := model.SearchUserTokens(targetUser.Id, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(buildMaskedTokenResponses(tokens))
	common.ApiSuccess(c, pageInfo)
}

func AdminGetUserToken(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	tid, err := strconv.Atoi(c.Param("tid"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(tid, targetUser.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

func AdminUpdateUserToken(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	statusOnly := c.Query("status_only")
	var token model.Token
	if err := c.ShouldBindJSON(&token); err != nil {
		common.ApiError(c, err)
		return
	}
	if !validateTokenWriteInput(c, &token) {
		return
	}

	cleanToken, err := model.GetTokenByIds(token.Id, targetUser.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	if token.Status == common.TokenStatusEnabled {
		if cleanToken.Status == common.TokenStatusExpired && cleanToken.ExpiredTime <= common.GetTimestamp() && cleanToken.ExpiredTime != -1 {
			common.ApiErrorI18n(c, i18n.MsgTokenExpiredCannotEnable)
			return
		}
		if cleanToken.Status == common.TokenStatusExhausted && cleanToken.RemainQuota <= 0 && !cleanToken.UnlimitedQuota {
			common.ApiErrorI18n(c, i18n.MsgTokenExhaustedCannotEable)
			return
		}
	}

	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		// Group safeguard: when assigning a group to a COMMON user's token, the
		// group must be in that user's usable set. Admin/root targets bypass this
		// (mirrors the runtime bypass at middleware/auth.go for admin-owned tokens).
		if token.Group != "" && targetUser.Role < common.RoleAdminUser {
			if !service.GroupInUserUsableGroups(targetUser.Group, token.Group) {
				common.ApiError(c, fmt.Errorf("group %q is not available to the target user", token.Group))
				return
			}
		}
		// Full-object replacement (parity with self-service UpdateToken).
		cleanToken.Name = token.Name
		cleanToken.ExpiredTime = token.ExpiredTime
		cleanToken.RemainQuota = token.RemainQuota
		cleanToken.UnlimitedQuota = token.UnlimitedQuota
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled
		cleanToken.ModelLimits = token.ModelLimits
		cleanToken.AllowIps = token.AllowIps
		cleanToken.Group = token.Group
		cleanToken.CrossGroupRetry = token.CrossGroupRetry
	}
	if err := cleanToken.Update(); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, targetUser.Id, "user.token.update", map[string]interface{}{
		"token_id": cleanToken.Id, "name": cleanToken.Name, "status_only": statusOnly != "",
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": buildMaskedTokenResponse(cleanToken)})
}
