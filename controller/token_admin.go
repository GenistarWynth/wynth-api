package controller

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

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

func AdminDeleteUserToken(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	tid, err := strconv.Atoi(c.Param("tid"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DeleteTokenById(tid, targetUser.Id); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, targetUser.Id, "user.token.delete", map[string]interface{}{"token_id": tid})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func AdminBatchDeleteUserTokens(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	var tokenBatch TokenBatch
	if err := c.ShouldBindJSON(&tokenBatch); err != nil || len(tokenBatch.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, targetUser.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, targetUser.Id, "user.token.batch_delete", map[string]interface{}{"count": count})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": count})
}

func AdminCreateUserToken(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	var token model.Token
	if err := c.ShouldBindJSON(&token); err != nil {
		common.ApiError(c, err)
		return
	}
	if !validateTokenWriteInput(c, &token) {
		return
	}
	if token.Group != "" && targetUser.Role < common.RoleAdminUser {
		if !service.GroupInUserUsableGroups(targetUser.Group, token.Group) {
			common.ApiError(c, fmt.Errorf("group %q is not available to the target user", token.Group))
			return
		}
	}
	maxTokens := operation_setting.GetMaxUserTokens()
	count, err := model.CountUserTokens(targetUser.Id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if int(count) >= maxTokens {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("已达到最大令牌数量限制 (%d)", maxTokens)})
		return
	}
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgTokenGenerateFailed)
		common.SysLog("failed to generate token key: " + err.Error())
		return
	}
	cleanToken := model.Token{
		UserId:             targetUser.Id,
		Name:               token.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
	}
	if err := cleanToken.Insert(); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAuditFor(c, targetUser.Id, "user.token.create", map[string]interface{}{"token_id": cleanToken.Id, "name": cleanToken.Name})
	// No plaintext key in the response — only root can reveal it via the reveal endpoint.
	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func AdminGetUserTokenKey(c *gin.Context) {
	targetUser, ok := resolveManageableTargetUser(c)
	if !ok {
		return
	}
	// Defense-in-depth: route also carries RootAuth, but enforce here too so the
	// behavior is correct and unit-testable without middleware.
	if c.GetInt("role") != common.RoleRootUser {
		common.ApiErrorI18n(c, i18n.MsgUserNoPermissionSameLevel)
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
	recordManageAuditFor(c, targetUser.Id, "user.token.key_view", map[string]interface{}{"token_id": token.Id, "name": token.Name})
	common.ApiSuccess(c, gin.H{"key": token.GetFullKey()})
}
