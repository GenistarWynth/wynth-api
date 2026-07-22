package controller

import (
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

const maxUpstreamSourceNotificationCooldownSeconds int64 = 30 * 24 * 60 * 60

func ListUpstreamSourceNotificationSubscriptions(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	var subscriptions []model.UpstreamSourceNotificationSubscription
	if err := model.DB.Where("user_id = ? AND source_id = ?", c.GetInt("id"), source.Id).Order("id").Find(&subscriptions).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, subscriptions)
}

func CreateUpstreamSourceNotificationSubscription(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	var req dto.UpstreamSourceNotificationSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	subscription, err := upstreamSourceNotificationSubscriptionFromRequest(c.GetInt("id"), source.Id, req, nil)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Create(subscription).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.notification_subscription_create", map[string]interface{}{
		"id": source.Id, "name": source.Name, "subscription_id": subscription.Id, "event_type": subscription.EventType,
	})
	common.ApiSuccess(c, subscription)
}

func UpdateUpstreamSourceNotificationSubscription(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	subscriptionID, err := strconv.Atoi(c.Param("subscription_id"))
	if err != nil || subscriptionID == 0 {
		common.ApiError(c, errors.New("invalid notification subscription id"))
		return
	}
	var existing model.UpstreamSourceNotificationSubscription
	if err := model.DB.Where("id = ? AND source_id = ? AND user_id = ?", subscriptionID, source.Id, c.GetInt("id")).First(&existing).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	var req dto.UpstreamSourceNotificationSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiError(c, err)
		return
	}
	subscription, err := upstreamSourceNotificationSubscriptionFromRequest(existing.UserID, existing.SourceID, req, &existing)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Model(&existing).Updates(map[string]interface{}{
		"event_type":       subscription.EventType,
		"group_id":         subscription.GroupID,
		"enabled":          subscription.Enabled,
		"cooldown_seconds": subscription.CooldownSeconds,
		"updated_at":       common.GetTimestamp(),
	}).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.First(&existing, subscriptionID).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "upstream_source.notification_subscription_update", map[string]interface{}{
		"id": source.Id, "name": source.Name, "subscription_id": existing.Id, "event_type": existing.EventType,
	})
	common.ApiSuccess(c, existing)
}

func DeleteUpstreamSourceNotificationSubscription(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	subscriptionID, err := strconv.Atoi(c.Param("subscription_id"))
	if err != nil || subscriptionID == 0 {
		common.ApiError(c, errors.New("invalid notification subscription id"))
		return
	}
	deleted := model.DB.Where("id = ? AND source_id = ? AND user_id = ?", subscriptionID, source.Id, c.GetInt("id")).Delete(&model.UpstreamSourceNotificationSubscription{})
	if deleted.Error != nil {
		common.ApiError(c, deleted.Error)
		return
	}
	if deleted.RowsAffected != 1 {
		common.ApiError(c, errors.New("notification subscription not found"))
		return
	}
	recordManageAudit(c, "upstream_source.notification_subscription_delete", map[string]interface{}{
		"id": source.Id, "name": source.Name, "subscription_id": subscriptionID,
	})
	common.ApiSuccess(c, nil)
}

func ListUpstreamSourceNotificationDeliveries(c *gin.Context) {
	source, ok := loadUpstreamSourceForController(c)
	if !ok {
		return
	}
	limit := upstreamSourceHistoryLimit(c)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	deliveries := make([]model.UpstreamSourceNotificationDelivery, 0, limit)
	if err := model.DB.Where("source_id = ?", source.Id).Order("id DESC").Limit(limit).Find(&deliveries).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, deliveries)
}

func upstreamSourceNotificationSubscriptionFromRequest(userID int, sourceID int, req dto.UpstreamSourceNotificationSubscriptionRequest, existing *model.UpstreamSourceNotificationSubscription) (*model.UpstreamSourceNotificationSubscription, error) {
	eventType := strings.TrimSpace(req.EventType)
	if eventType == "" && existing != nil {
		eventType = existing.EventType
	}
	if !validUpstreamSourceNotificationEventType(eventType) {
		return nil, errors.New("unsupported upstream source notification event type")
	}
	if req.CooldownSeconds < 0 || req.CooldownSeconds > maxUpstreamSourceNotificationCooldownSeconds {
		return nil, errors.New("notification cooldown seconds must be between 0 and 2592000")
	}
	enabled := true
	if existing != nil {
		enabled = existing.Enabled
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return &model.UpstreamSourceNotificationSubscription{
		UserID:          userID,
		SourceID:        sourceID,
		EventType:       eventType,
		GroupID:         strings.TrimSpace(req.GroupID),
		Enabled:         enabled,
		CooldownSeconds: req.CooldownSeconds,
	}, nil
}

func validUpstreamSourceNotificationEventType(eventType string) bool {
	switch eventType {
	case model.UpstreamSourceNotificationEventAll,
		model.UpstreamSourceNotificationEventGroupAdded,
		model.UpstreamSourceNotificationEventGroupRemoved,
		model.UpstreamSourceNotificationEventGroupRestored,
		model.UpstreamSourceNotificationEventRateChanged,
		model.UpstreamSourceNotificationEventAnnouncementNew,
		model.UpstreamSourceNotificationEventSubscriptionRemainingLow,
		model.UpstreamSourceNotificationEventSubscriptionExpiring,
		model.UpstreamSourceNotificationEventBalanceLow:
		return true
	default:
		return false
	}
}
