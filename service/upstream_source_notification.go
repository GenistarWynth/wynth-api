package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

const (
	defaultUpstreamSourceNotificationAttempts                 = 3
	defaultUpstreamSourceNotificationBackoff                  = 100 * time.Millisecond
	defaultUpstreamSourceNoisyEventCooldownSeconds      int64 = 24 * 60 * 60
	defaultUpstreamSourceSubscriptionRemainingThreshold       = 20.0
	defaultUpstreamSourceSubscriptionExpiryThreshold    int64 = 7 * 24 * 60 * 60
)

type upstreamSourceMonitorNotificationEvent struct {
	eventType       string
	groupID         string
	batchKey        string
	dedupKey        string
	summary         string
	cooldownSeconds int64
}

type UpstreamSourceMonitorNotifier struct {
	Now   func() int64
	Sleep func(time.Duration)
	Send  func(userID int, userEmail string, setting dto.UserSetting, notification dto.Notify) error
}

func (notifier UpstreamSourceMonitorNotifier) NotifyScan(ctx context.Context, source *model.UpstreamSource, scanID int) error {
	if source == nil || source.Id == 0 || scanID == 0 {
		return errors.New("persisted source and scan ID are required")
	}
	events, err := buildUpstreamSourceMonitorNotificationEvents(source.Id, scanID, notifier.now())
	if err != nil || len(events) == 0 {
		return err
	}

	var users []model.User
	if err := model.DB.Select("id", "email", "setting").
		Where("status = ? AND role >= ?", common.UserStatusEnabled, common.RoleAdminUser).
		Order("id").Find(&users).Error; err != nil {
		return err
	}
	userIDs := make([]int, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.Id)
	}
	var subscriptions []model.UpstreamSourceNotificationSubscription
	if len(userIDs) > 0 {
		if err := model.DB.Where("user_id IN ?", userIDs).Order("id").Find(&subscriptions).Error; err != nil {
			return err
		}
	}
	subscriptionsByUser := make(map[int][]model.UpstreamSourceNotificationSubscription, len(users))
	for _, subscription := range subscriptions {
		subscriptionsByUser[subscription.UserID] = append(subscriptionsByUser[subscription.UserID], subscription)
	}

	for _, user := range users {
		eligible := make([]upstreamSourceMonitorNotificationEvent, 0, len(events))
		for _, event := range events {
			matched, cooldownSeconds := upstreamSourceNotificationSubscriptionMatch(subscriptionsByUser[user.Id], source.Id, event)
			if !matched {
				continue
			}
			if event.cooldownSeconds > 0 && cooldownSeconds == 0 {
				cooldownSeconds = event.cooldownSeconds
			}
			event.cooldownSeconds = cooldownSeconds
			eligible = append(eligible, event)
		}
		if err := notifier.notifyUser(ctx, user, source, scanID, eligible); err != nil {
			return err
		}
	}

	// IsNew describes a collector event, not permanent announcement state. Consume
	// it after every configured recipient has been evaluated so audit retention does
	// not make an old announcement eligible again later.
	return model.DB.Model(&model.UpstreamSourceAnnouncement{}).
		Where("source_id = ? AND scan_id = ? AND is_new = ?", source.Id, scanID, true).
		Update("is_new", false).Error
}

func (notifier UpstreamSourceMonitorNotifier) notifyUser(ctx context.Context, user model.User, source *model.UpstreamSource, scanID int, events []upstreamSourceMonitorNotificationEvent) error {
	batched := make(map[string][]upstreamSourceMonitorNotificationEvent)
	batchOrder := make([]string, 0)
	for _, event := range events {
		batchKey := event.batchKey
		if batchKey == "" {
			batchKey = event.dedupKey
		}
		if _, exists := batched[batchKey]; !exists {
			batchOrder = append(batchOrder, batchKey)
		}
		batched[batchKey] = append(batched[batchKey], event)
	}

	for _, batchKey := range batchOrder {
		batchEvents := batched[batchKey]
		if len(batchEvents) == 0 {
			continue
		}
		eventKey := batchEvents[0].dedupKey
		eventType := batchEvents[0].eventType
		if len(batchEvents) > 1 && batchEvents[0].batchKey != "" {
			eventKey = batchEvents[0].batchKey
			eventType = model.UpstreamSourceNotificationEventRateGroupBatch
		}
		var successfulDeliveries int64
		if err := model.DB.Model(&model.UpstreamSourceNotificationDelivery{}).
			Where("user_id = ? AND source_id = ? AND event_key = ? AND status = ?", user.Id, source.Id, eventKey, model.UpstreamSourceNotificationDeliverySuccess).
			Count(&successfulDeliveries).Error; err != nil {
			return err
		}
		if successfulDeliveries > 0 {
			continue
		}

		readyEvents := make([]upstreamSourceMonitorNotificationEvent, 0, len(batchEvents))
		for _, event := range batchEvents {
			key := model.UpstreamSourceNotificationCooldownKey{UserID: user.Id, SourceID: source.Id, EventType: event.eventType, GroupID: event.groupID}
			ready, err := model.IsUpstreamSourceNotificationCooldownReady(key, notifier.now(), event.cooldownSeconds)
			if err != nil {
				return err
			}
			if ready {
				readyEvents = append(readyEvents, event)
			}
		}
		if len(readyEvents) == 0 {
			continue
		}

		summaries := make([]string, 0, len(readyEvents))
		for _, event := range readyEvents {
			summaries = append(summaries, event.summary)
		}
		notification := dto.NewNotify(
			dto.NotifyTypeUpstreamSourceMonitor,
			"Upstream source monitor: "+sanitizeUpstreamSourceNotificationText(source.Name),
			sanitizeUpstreamSourceNotificationText(strings.Join(summaries, "\n")),
			nil,
		)
		attempts, sendErr := notifier.deliver(ctx, user, notification)
		audit := model.UpstreamSourceNotificationDelivery{
			UserID:    user.Id,
			SourceID:  source.Id,
			ScanID:    scanID,
			EventType: eventType,
			EventKey:  eventKey,
			Attempts:  attempts,
			CreatedAt: notifier.now(),
		}
		if sendErr != nil {
			audit.Status = model.UpstreamSourceNotificationDeliveryFailed
			audit.ErrorSummary = SanitizeUpstreamSourceError(sendErr)
			if err := model.DB.Create(&audit).Error; err != nil {
				return err
			}
			continue
		}
		audit.Status = model.UpstreamSourceNotificationDeliverySuccess
		if err := model.DB.Create(&audit).Error; err != nil {
			return err
		}
		for _, event := range readyEvents {
			if event.cooldownSeconds <= 0 {
				continue
			}
			key := model.UpstreamSourceNotificationCooldownKey{UserID: user.Id, SourceID: source.Id, EventType: event.eventType, GroupID: event.groupID}
			if err := model.RecordUpstreamSourceNotificationCooldown(key, notifier.now()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (notifier UpstreamSourceMonitorNotifier) deliver(ctx context.Context, user model.User, notification dto.Notify) (int, error) {
	send := notifier.Send
	if send == nil {
		if err := upstreamSourceNotificationDeliveryConfigurationError(user); err != nil {
			return 0, err
		}
		send = NotifyUser
	}
	var err error
	for attempt := 1; attempt <= defaultUpstreamSourceNotificationAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return attempt - 1, ctxErr
		}
		err = send(user.Id, user.Email, user.GetSetting(), notification)
		if err == nil {
			return attempt, nil
		}
		if attempt == defaultUpstreamSourceNotificationAttempts {
			break
		}
		notifier.sleep(defaultUpstreamSourceNotificationBackoff * time.Duration(1<<(attempt-1)))
	}
	return defaultUpstreamSourceNotificationAttempts, err
}

func upstreamSourceNotificationDeliveryConfigurationError(user model.User) error {
	setting := user.GetSetting()
	notifyType := setting.NotifyType
	if notifyType == "" {
		notifyType = dto.NotifyTypeEmail
	}
	switch notifyType {
	case dto.NotifyTypeEmail:
		if strings.TrimSpace(setting.NotificationEmail) == "" && strings.TrimSpace(user.Email) == "" {
			return errors.New("notification email is not configured")
		}
	case dto.NotifyTypeWebhook:
		if strings.TrimSpace(setting.WebhookUrl) == "" {
			return errors.New("notification webhook URL is not configured")
		}
	case dto.NotifyTypeBark:
		if strings.TrimSpace(setting.BarkUrl) == "" {
			return errors.New("notification Bark URL is not configured")
		}
	case dto.NotifyTypeGotify:
		if strings.TrimSpace(setting.GotifyUrl) == "" || strings.TrimSpace(setting.GotifyToken) == "" {
			return errors.New("notification Gotify URL or token is not configured")
		}
	default:
		return fmt.Errorf("unsupported notification transport %q", notifyType)
	}
	return nil
}

func (notifier UpstreamSourceMonitorNotifier) now() int64 {
	if notifier.Now != nil {
		return notifier.Now()
	}
	return common.GetTimestamp()
}

func (notifier UpstreamSourceMonitorNotifier) sleep(delay time.Duration) {
	if notifier.Sleep != nil {
		notifier.Sleep(delay)
		return
	}
	time.Sleep(delay)
}

func upstreamSourceNotificationSubscriptionMatch(subscriptions []model.UpstreamSourceNotificationSubscription, sourceID int, event upstreamSourceMonitorNotificationEvent) (bool, int64) {
	matched := false
	relevantRules := 0
	cooldownSeconds := int64(0)
	for _, subscription := range subscriptions {
		if subscription.SourceID != 0 && subscription.SourceID != sourceID {
			continue
		}
		relevantRules++
		if !subscription.Matches(sourceID, event.eventType, event.groupID) {
			continue
		}
		matched = true
		if subscription.CooldownSeconds > 0 && (cooldownSeconds == 0 || subscription.CooldownSeconds < cooldownSeconds) {
			cooldownSeconds = subscription.CooldownSeconds
		}
	}
	if relevantRules == 0 {
		return true, event.cooldownSeconds
	}
	return matched, cooldownSeconds
}

func buildUpstreamSourceMonitorNotificationEvents(sourceID int, scanID int, now int64) ([]upstreamSourceMonitorNotificationEvent, error) {
	events := make([]upstreamSourceMonitorNotificationEvent, 0)
	var changes []model.UpstreamSourceGroupChange
	if err := model.DB.Where("source_id = ? AND scan_id = ?", sourceID, scanID).Order("id").Find(&changes).Error; err != nil {
		return nil, err
	}
	for _, change := range changes {
		eventType := upstreamSourceNotificationEventTypeForChange(change.ChangeType)
		if eventType == "" {
			continue
		}
		groupName := strings.TrimSpace(change.UpstreamGroupName)
		if groupName == "" {
			groupName = change.UpstreamGroupID
		}
		events = append(events, upstreamSourceMonitorNotificationEvent{
			eventType: eventType,
			groupID:   change.UpstreamGroupID,
			batchKey:  fmt.Sprintf("scan:%d:rate-group", scanID),
			dedupKey:  fmt.Sprintf("change:%d", change.Id),
			summary:   upstreamSourceGroupChangeSummary(change, groupName),
		})
	}

	var announcements []model.UpstreamSourceAnnouncement
	if err := model.DB.Where("source_id = ? AND scan_id = ? AND is_new = ?", sourceID, scanID, true).Order("id").Find(&announcements).Error; err != nil {
		return nil, err
	}
	for _, announcement := range announcements {
		summary := "New announcement: " + announcement.Title
		if strings.TrimSpace(announcement.Content) != "" {
			summary += " — " + announcement.Content
		}
		events = append(events, upstreamSourceMonitorNotificationEvent{
			eventType: model.UpstreamSourceNotificationEventAnnouncementNew,
			dedupKey:  fmt.Sprintf("announcement:%d", announcement.Id),
			summary:   summary,
		})
	}

	var snapshots []model.UpstreamSourceSubscriptionUsageSnapshot
	if err := model.DB.Where("source_id = ? AND scan_id = ?", sourceID, scanID).Order("id").Find(&snapshots).Error; err != nil {
		return nil, err
	}
	expiringSubscriptions := make(map[string]struct{})
	for _, snapshot := range snapshots {
		name := strings.TrimSpace(snapshot.Name)
		if name == "" {
			name = snapshot.SubscriptionKey
		}
		if snapshot.RemainingPercent != nil && *snapshot.RemainingPercent <= defaultUpstreamSourceSubscriptionRemainingThreshold {
			events = append(events, upstreamSourceMonitorNotificationEvent{
				eventType:       model.UpstreamSourceNotificationEventSubscriptionRemainingLow,
				groupID:         snapshot.SubscriptionKey + ":" + snapshot.Window,
				dedupKey:        fmt.Sprintf("scan:%d:subscription-low:%s:%s", scanID, snapshot.SubscriptionKey, snapshot.Window),
				summary:         fmt.Sprintf("Subscription remaining low: %s (%s %.2f%% remaining)", name, snapshot.Window, *snapshot.RemainingPercent),
				cooldownSeconds: defaultUpstreamSourceNoisyEventCooldownSeconds,
			})
		}
		if snapshot.ExpiresAt <= 0 || snapshot.ExpiresAt > now+defaultUpstreamSourceSubscriptionExpiryThreshold {
			continue
		}
		if _, exists := expiringSubscriptions[snapshot.SubscriptionKey]; exists {
			continue
		}
		expiringSubscriptions[snapshot.SubscriptionKey] = struct{}{}
		events = append(events, upstreamSourceMonitorNotificationEvent{
			eventType:       model.UpstreamSourceNotificationEventSubscriptionExpiring,
			groupID:         snapshot.SubscriptionKey,
			dedupKey:        fmt.Sprintf("scan:%d:subscription-expiring:%s", scanID, snapshot.SubscriptionKey),
			summary:         fmt.Sprintf("Subscription expiring: %s (expires at %d)", name, snapshot.ExpiresAt),
			cooldownSeconds: defaultUpstreamSourceNoisyEventCooldownSeconds,
		})
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].batchKey == events[j].batchKey {
			return events[i].dedupKey < events[j].dedupKey
		}
		return events[i].batchKey < events[j].batchKey
	})
	return events, nil
}

func upstreamSourceNotificationEventTypeForChange(changeType string) string {
	switch changeType {
	case model.UpstreamSourceGroupChangeAdded:
		return model.UpstreamSourceNotificationEventGroupAdded
	case model.UpstreamSourceGroupChangeRemoved:
		return model.UpstreamSourceNotificationEventGroupRemoved
	case model.UpstreamSourceGroupChangeRestored:
		return model.UpstreamSourceNotificationEventGroupRestored
	case model.UpstreamSourceGroupChangeRateChanged:
		return model.UpstreamSourceNotificationEventRateChanged
	default:
		return ""
	}
}

func upstreamSourceGroupChangeSummary(change model.UpstreamSourceGroupChange, groupName string) string {
	switch change.ChangeType {
	case model.UpstreamSourceGroupChangeAdded:
		return "Group added: " + groupName
	case model.UpstreamSourceGroupChangeRemoved:
		return "Group removed: " + groupName
	case model.UpstreamSourceGroupChangeRestored:
		return "Group restored: " + groupName
	case model.UpstreamSourceGroupChangeRateChanged:
		return fmt.Sprintf("Rate changed: %s (%s → %s)", groupName, upstreamSourceRateText(change.OldEffectiveRateMultiplier), upstreamSourceRateText(change.NewEffectiveRateMultiplier))
	default:
		return "Group changed: " + groupName
	}
}

func upstreamSourceRateText(rate *float64) string {
	if rate == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.4g", *rate)
}

func sanitizeUpstreamSourceNotificationText(text string) string {
	return SanitizeUpstreamSourceError(errors.New(text))
}

func NotifyUpstreamSourceMonitorScan(ctx context.Context, source *model.UpstreamSource, scanID int) error {
	return (UpstreamSourceMonitorNotifier{}).NotifyScan(ctx, source, scanID)
}
