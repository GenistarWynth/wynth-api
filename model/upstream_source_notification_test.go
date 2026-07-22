package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSourceNotificationSubscriptionMatchesSourceEventAndGroup(t *testing.T) {
	tests := []struct {
		name         string
		subscription UpstreamSourceNotificationSubscription
		sourceID     int
		eventType    string
		groupID      string
		want         bool
	}{
		{
			name: "exact source event and group",
			subscription: UpstreamSourceNotificationSubscription{
				SourceID:  10,
				EventType: UpstreamSourceNotificationEventRateChanged,
				GroupID:   "premium",
				Enabled:   true,
			},
			sourceID:  10,
			eventType: UpstreamSourceNotificationEventRateChanged,
			groupID:   "premium",
			want:      true,
		},
		{
			name: "wildcard source event and group",
			subscription: UpstreamSourceNotificationSubscription{
				EventType: UpstreamSourceNotificationEventAll,
				Enabled:   true,
			},
			sourceID:  22,
			eventType: UpstreamSourceNotificationEventAnnouncementNew,
			groupID:   "standard",
			want:      true,
		},
		{
			name: "different source",
			subscription: UpstreamSourceNotificationSubscription{
				SourceID:  10,
				EventType: UpstreamSourceNotificationEventRateChanged,
				Enabled:   true,
			},
			sourceID:  11,
			eventType: UpstreamSourceNotificationEventRateChanged,
			want:      false,
		},
		{
			name: "different event",
			subscription: UpstreamSourceNotificationSubscription{
				SourceID:  10,
				EventType: UpstreamSourceNotificationEventRateChanged,
				Enabled:   true,
			},
			sourceID:  10,
			eventType: UpstreamSourceNotificationEventGroupAdded,
			want:      false,
		},
		{
			name: "different group",
			subscription: UpstreamSourceNotificationSubscription{
				SourceID:  10,
				EventType: UpstreamSourceNotificationEventRateChanged,
				GroupID:   "premium",
				Enabled:   true,
			},
			sourceID:  10,
			eventType: UpstreamSourceNotificationEventRateChanged,
			groupID:   "standard",
			want:      false,
		},
		{
			name: "disabled rule",
			subscription: UpstreamSourceNotificationSubscription{
				EventType: UpstreamSourceNotificationEventAll,
			},
			sourceID:  10,
			eventType: UpstreamSourceNotificationEventGroupAdded,
			want:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.subscription.Matches(test.sourceID, test.eventType, test.groupID))
		})
	}
}

func TestUpstreamSourceNotificationCooldownIsDurable(t *testing.T) {
	setupUpstreamSourceTestDB(t)

	key := UpstreamSourceNotificationCooldownKey{
		UserID:    7,
		SourceID:  3,
		EventType: UpstreamSourceNotificationEventBalanceLow,
		GroupID:   "wallet",
	}

	allowed, err := IsUpstreamSourceNotificationCooldownReady(key, 1000, 3600)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, RecordUpstreamSourceNotificationCooldown(key, 1000))

	allowed, err = IsUpstreamSourceNotificationCooldownReady(key, 1200, 3600)
	require.NoError(t, err)
	assert.False(t, allowed)

	allowed, err = IsUpstreamSourceNotificationCooldownReady(key, 4600, 3600)
	require.NoError(t, err)
	assert.True(t, allowed)
}
