package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fakeUpstreamBalanceAdapter struct {
	fakeUpstreamSourceAdapter
	collect func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error)
}

type fakeUpstreamCollectorsAdapter struct {
	fakeUpstreamSourceAdapter
	balance       func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error)
	cost          func(context.Context, *model.UpstreamSource) (UpstreamCostSnapshot, error)
	rates         func(context.Context, *model.UpstreamSource) (UpstreamRateGroupSnapshot, error)
	announcements func(context.Context, *model.UpstreamSource) (UpstreamAnnouncementSnapshot, error)
	subscription  func(context.Context, *model.UpstreamSource) (UpstreamSubscriptionUsageSnapshot, error)
}

func (a fakeUpstreamCollectorsAdapter) CollectBalance(ctx context.Context, source *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
	if a.balance == nil {
		return UpstreamBalanceSnapshot{}, nil
	}
	return a.balance(ctx, source)
}

func (a fakeUpstreamCollectorsAdapter) CollectCost(ctx context.Context, source *model.UpstreamSource) (UpstreamCostSnapshot, error) {
	if a.cost == nil {
		return UpstreamCostSnapshot{Currency: "USD"}, nil
	}
	return a.cost(ctx, source)
}

func (a fakeUpstreamCollectorsAdapter) CollectRateGroups(ctx context.Context, source *model.UpstreamSource) (UpstreamRateGroupSnapshot, error) {
	if a.rates == nil {
		return UpstreamRateGroupSnapshot{Groups: []UpstreamGroup{}}, nil
	}
	return a.rates(ctx, source)
}

func (a fakeUpstreamCollectorsAdapter) CollectAnnouncements(ctx context.Context, source *model.UpstreamSource) (UpstreamAnnouncementSnapshot, error) {
	if a.announcements == nil {
		return UpstreamAnnouncementSnapshot{Items: []UpstreamAnnouncement{}}, nil
	}
	return a.announcements(ctx, source)
}

func (a fakeUpstreamCollectorsAdapter) CollectSubscriptionUsage(ctx context.Context, source *model.UpstreamSource) (UpstreamSubscriptionUsageSnapshot, error) {
	if a.subscription == nil {
		return UpstreamSubscriptionUsageSnapshot{Subscriptions: []UpstreamSubscriptionUsage{}}, nil
	}
	return a.subscription(ctx, source)
}

func (a fakeUpstreamBalanceAdapter) CollectBalance(ctx context.Context, source *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
	return a.collect(ctx, source)
}

func TestUpstreamSourceCollectorCapabilitiesAreOptional(t *testing.T) {
	adapter := any(fakeUpstreamSourceAdapter{})

	_, hasBalance := adapter.(UpstreamBalanceCollector)
	_, hasCost := adapter.(UpstreamCostCollector)
	_, hasRates := adapter.(UpstreamRateGroupCollector)
	_, hasAnnouncements := adapter.(UpstreamAnnouncementCollector)
	_, hasSubscriptionUsage := adapter.(UpstreamSubscriptionUsageCollector)

	assert.False(t, hasBalance)
	assert.False(t, hasCost)
	assert.False(t, hasRates)
	assert.False(t, hasAnnouncements)
	assert.False(t, hasSubscriptionUsage)
}

func TestListDueUpstreamSourcesForMonitorDefaultsDisabledAndUsesSchedule(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	sources := []model.UpstreamSource{
		{Name: "legacy-default-disabled", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://one.example.com"},
		{Name: "legacy-enabled-zero", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://two.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 0},
		{Name: "due", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://three.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 900},
		{Name: "future", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://four.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1001},
		{Name: "disabled-source", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusDisabled, BaseURL: "https://five.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 0},
	}
	require.NoError(t, model.DB.Create(&sources).Error)

	due, err := model.ListDueUpstreamSourcesForMonitor(1000, 100)
	require.NoError(t, err)
	require.Len(t, due, 2)
	assert.Equal(t, sources[1].Id, due[0].Id)
	assert.Equal(t, sources[2].Id, due[1].Id)
}

func TestClaimUpstreamSourceMonitorPreventsTwoWorkersFromClaimingSameSource(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "claim-once",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://claim.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)

	claimedA, err := model.ClaimUpstreamSourceMonitor(source.Id, "worker-a", 1000, 60)
	require.NoError(t, err)
	claimedB, err := model.ClaimUpstreamSourceMonitor(source.Id, "worker-b", 1000, 60)
	require.NoError(t, err)

	assert.True(t, claimedA)
	assert.False(t, claimedB)
}

func TestClaimUpstreamSourceMonitorRejectsParkedSourceFromStaleDueList(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "parked",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://parked.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          0,
		MonitorParkedReason:    model.UpstreamSourceMonitorParkedReasonCredentialDecryption,
	}
	require.NoError(t, model.DB.Create(&source).Error)

	claimed, err := model.ClaimUpstreamSourceMonitor(source.Id, "stale-worker", 1000, 60)

	require.NoError(t, err)
	assert.False(t, claimed)
}

func TestReleaseUpstreamSourceMonitorParksDecryptFailureBeyondOldWorkerPredicate(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "rolling-safe-park",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://rolling-safe.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	claimed, err := model.ClaimUpstreamSourceMonitor(source.Id, "worker", 1000, 60)
	require.NoError(t, err)
	require.True(t, claimed)

	require.NoError(t, model.ReleaseUpstreamSourceMonitor(source.Id, "worker", 1200, false))

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamSourceMonitorParkedSchedule, reloaded.NextMonitorAt)
	assert.Equal(t, model.UpstreamSourceMonitorParkedReasonCredentialDecryption, reloaded.MonitorParkedReason)
	var oldWorkerDue int64
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).
		Where("id = ? AND status = ? AND monitor_enabled = ? AND next_monitor_at <= ?", source.Id, model.UpstreamSourceStatusEnabled, true, int64(2000)).
		Count(&oldWorkerDue).Error)
	assert.Zero(t, oldWorkerDue, "the pre-upgrade due predicate must not claim an explicitly parked source")
}

func TestReleaseUpstreamSourceMonitorDoesNotRescheduleSourceDisabledMidRun(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "disabled-mid-run",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://disable.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	claimed, err := model.ClaimUpstreamSourceMonitor(source.Id, "worker", 1000, 60)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = UpdateUpstreamSourceMonitorSettings(source.Id, false, 10, 1100)
	require.NoError(t, err)

	require.NoError(t, model.ReleaseUpstreamSourceMonitor(source.Id, "worker", 1200, true))

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.False(t, reloaded.MonitorEnabled)
	assert.Zero(t, reloaded.NextMonitorAt)
	assert.Empty(t, reloaded.CurrentMonitorToken)
}

func TestUpdateUpstreamSourceMonitorSettingsClearsParkWithoutDroppingClaim(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "settings-clear-park",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://settings-clear-park.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          model.UpstreamSourceMonitorParkedSchedule,
		MonitorParkedReason:    model.UpstreamSourceMonitorParkedReasonCredentialDecryption,
		CurrentMonitorToken:    "active-worker",
		MonitorStartedAt:       1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)

	disabled, err := UpdateUpstreamSourceMonitorSettings(source.Id, false, 10, 1500)
	require.NoError(t, err)
	assert.False(t, disabled.MonitorEnabled)
	assert.Zero(t, disabled.NextMonitorAt)
	assert.Equal(t, "active-worker", disabled.CurrentMonitorToken)
	assert.Empty(t, disabled.MonitorParkedReason)

	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]interface{}{
		"next_monitor_at":       model.UpstreamSourceMonitorParkedSchedule,
		"monitor_parked_reason": model.UpstreamSourceMonitorParkedReasonCredentialDecryption,
	}).Error)
	enabled, err := UpdateUpstreamSourceMonitorSettings(source.Id, true, 10, 2000)
	require.NoError(t, err)
	assert.True(t, enabled.MonitorEnabled)
	assert.Equal(t, int64(2000), enabled.NextMonitorAt)
	assert.Equal(t, "active-worker", enabled.CurrentMonitorToken)
	assert.Empty(t, enabled.MonitorParkedReason)
}

func TestReconcileStaleUpstreamSourceMonitorRunsFinalizesScanAndReleasesClaim(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "stale-monitor",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://stale.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		CurrentMonitorToken:    "crashed-worker",
		MonitorStartedAt:       100,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	scan := model.UpstreamSourceScan{
		SourceID:  source.Id,
		ScanType:  model.UpstreamSourceScanTypeMonitor,
		Status:    model.UpstreamSourceScanStatusRunning,
		StartedAt: 100,
	}
	require.NoError(t, model.DB.Create(&scan).Error)

	reconciled, err := model.ReconcileStaleUpstreamSourceMonitorRuns(1000, 60)
	require.NoError(t, err)
	assert.Equal(t, int64(1), reconciled)

	var reloadedScan model.UpstreamSourceScan
	require.NoError(t, model.DB.First(&reloadedScan, scan.Id).Error)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, reloadedScan.Status)
	assert.Equal(t, int64(1000), reloadedScan.FinishedAt)
	assert.Contains(t, reloadedScan.ErrorSummary, "interrupted")
	var reloadedSource model.UpstreamSource
	require.NoError(t, model.DB.First(&reloadedSource, source.Id).Error)
	assert.Empty(t, reloadedSource.CurrentMonitorToken)
}

func TestReconcileStaleUpstreamSourceMonitorKeepsExplicitParkRollingSafe(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "stale-parked-monitor",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://stale-parked.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          model.UpstreamSourceMonitorParkedSchedule,
		MonitorParkedReason:    model.UpstreamSourceMonitorParkedReasonCredentialDecryption,
		CurrentMonitorToken:    "crashed-worker",
		MonitorStartedAt:       100,
	}
	require.NoError(t, model.DB.Create(&source).Error)

	_, err := model.ReconcileStaleUpstreamSourceMonitorRuns(1000, 60)
	require.NoError(t, err)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Empty(t, reloaded.CurrentMonitorToken)
	assert.Equal(t, model.UpstreamSourceMonitorParkedSchedule, reloaded.NextMonitorAt)
	assert.Equal(t, model.UpstreamSourceMonitorParkedReasonCredentialDecryption, reloaded.MonitorParkedReason)
	var oldWorkerDue int64
	require.NoError(t, model.DB.Model(&model.UpstreamSource{}).
		Where("id = ? AND monitor_enabled = ? AND next_monitor_at <= ?", source.Id, true, int64(1000)).
		Count(&oldWorkerDue).Error)
	assert.Zero(t, oldWorkerDue)
}

func TestUpstreamSourceMonitorRunnerBoundsConcurrency(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	sources := []model.UpstreamSource{
		{Name: "one", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://one.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1000},
		{Name: "two", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://two.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1000},
		{Name: "three", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://three.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1000},
	}
	require.NoError(t, model.DB.Create(&sources).Error)

	started := make(chan int, len(sources))
	var active atomic.Int32
	var maximum atomic.Int32
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(ctx context.Context, source *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				current := active.Add(1)
				defer active.Add(-1)
				for {
					observed := maximum.Load()
					if current <= observed || maximum.CompareAndSwap(observed, current) {
						break
					}
				}
				started <- source.Id
				<-ctx.Done()
				return UpstreamBalanceSnapshot{}, ctx.Err()
			}}, nil
		},
		MaxConcurrency: 2,
		SourceTimeout:  time.Minute,
		BatchTimeout:   time.Minute,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []UpstreamSourceMonitorResult, 1)
	go func() { done <- runner.RunDue(ctx, 1000) }()

	<-started
	<-started
	assert.Equal(t, int32(2), maximum.Load())
	select {
	case unexpected := <-started:
		t.Fatalf("source %d started beyond the concurrency bound", unexpected)
	default:
	}
	cancel()
	results := <-done
	assert.LessOrEqual(t, maximum.Load(), int32(2))
	assert.Len(t, results, 2, "the queued source must not start after the whole batch is canceled")
}

func TestUpstreamSourceMonitorRunnerIsolatesSourceFailures(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	sources := []model.UpstreamSource{
		{Name: "success-one", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://one.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1000},
		{Name: "fails", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://two.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1000},
		{Name: "success-two", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://three.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1000},
	}
	require.NoError(t, model.DB.Create(&sources).Error)
	failingID := sources[1].Id
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(ctx context.Context, source *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				if source.Id == failingID {
					return UpstreamBalanceSnapshot{}, errors.New("balance failed with access_token=collector-secret")
				}
				return UpstreamBalanceSnapshot{Available: 10}, nil
			}}, nil
		},
		MaxConcurrency: 2,
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 3)
	statuses := make(map[int]string, len(results))
	for _, result := range results {
		statuses[result.SourceID] = result.Status
		assert.NotContains(t, result.Error, "collector-secret")
	}
	assert.Equal(t, model.UpstreamSourceScanStatusSuccess, statuses[sources[0].Id])
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, statuses[sources[1].Id])
	assert.Equal(t, model.UpstreamSourceScanStatusSuccess, statuses[sources[2].Id])

	var running int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceScan{}).Where("scan_type = ? AND status = ?", model.UpstreamSourceScanTypeMonitor, model.UpstreamSourceScanStatusRunning).Count(&running).Error)
	assert.Zero(t, running)
}

func TestUpstreamSourceMonitorRunnerFinalizesTimedOutScan(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "times-out",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://timeout.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(ctx context.Context, source *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				<-ctx.Done()
				return UpstreamBalanceSnapshot{}, ctx.Err()
			}}, nil
		},
		Now:           func() int64 { return 5000 },
		SourceTimeout: 5 * time.Millisecond,
		BatchTimeout:  time.Second,
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
	assert.Contains(t, results[0].Error, "deadline exceeded")

	var scan model.UpstreamSourceScan
	require.NoError(t, model.DB.First(&scan, results[0].ScanID).Error)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, scan.Status)
	assert.Equal(t, int64(5000), scan.FinishedAt)
	assert.Contains(t, scan.ErrorSummary, "deadline exceeded")
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Empty(t, reloaded.CurrentMonitorToken)
}

func TestUpstreamSourceMonitorRunnerHonorsWholeBatchDeadline(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "batch-times-out",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://batch-timeout.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(ctx context.Context, source *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				<-ctx.Done()
				return UpstreamBalanceSnapshot{}, ctx.Err()
			}}, nil
		},
		Now:           func() int64 { return 5500 },
		SourceTimeout: time.Minute,
		BatchTimeout:  5 * time.Millisecond,
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
	assert.Contains(t, results[0].Error, "deadline exceeded")
	var scan model.UpstreamSourceScan
	require.NoError(t, model.DB.First(&scan, results[0].ScanID).Error)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, scan.Status)
	assert.Equal(t, int64(5500), scan.FinishedAt)
}

func TestUpstreamSourceMonitorRunnerSafelySucceedsWithNoSupportedCollectors(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "unsupported-collectors",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://none.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{}, nil
		},
		Now: func() int64 { return 6000 },
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusSuccess, results[0].Status)
	assert.Zero(t, results[0].Collected)
	assert.Zero(t, results[0].Failed)
}

func TestUpstreamSourceMonitorRunnerRecordsUnsupportedCapabilities(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "unsupported-capabilities",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://none.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) { return fakeUpstreamSourceAdapter{}, nil },
		Now:            func() int64 { return 2000 },
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusSuccess, results[0].Status)
	assert.Equal(t, 5, results[0].Unsupported)
	assert.Zero(t, results[0].Failed)

	var outcomes []model.UpstreamSourceCapabilityOutcome
	require.NoError(t, model.DB.Where("scan_id = ?", results[0].ScanID).Order("id").Find(&outcomes).Error)
	require.Len(t, outcomes, 5)
	for _, outcome := range outcomes {
		assert.Equal(t, model.UpstreamSourceCapabilityStatusUnsupported, outcome.Status)
		assert.Empty(t, outcome.ErrorSummary)
	}
}

func TestUpstreamSourceMonitorRunnerPersistsCollectedBalance(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "balance-persistence",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://balance.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				return UpstreamBalanceSnapshot{Available: 42.5, Currency: "USD", CollectedAt: 1100}, nil
			}}, nil
		},
		Now: func() int64 { return 2000 },
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].Collected)
	assert.Equal(t, 4, results[0].Unsupported)
	var balance model.UpstreamSourceBalanceSnapshot
	require.NoError(t, model.DB.First(&balance, "source_id = ?", source.Id).Error)
	assert.Equal(t, 42.5, balance.Available)
	assert.Equal(t, int64(1100), balance.CollectedAt)
}

func TestUpstreamSourceMonitorRunnerNotifiesAfterCollectorEventsPersist(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "notification-wiring",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://notify.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	oldRate := 1.0
	require.NoError(t, model.DB.Create(&model.UpstreamSourceChannelMapping{
		SourceID:                source.Id,
		UpstreamGroupID:         "premium",
		UpstreamGroupName:       "Premium",
		DiscoveryStatus:         model.UpstreamMappingDiscoveryStatusActive,
		UpstreamRateMultiplier:  &oldRate,
		EffectiveRateMultiplier: &oldRate,
	}).Error)
	require.NoError(t, model.DB.Create(&model.UpstreamSourceScan{
		SourceID: source.Id, ScanType: model.UpstreamSourceScanTypeDiscover, Status: model.UpstreamSourceScanStatusSuccess, StartedAt: 100, FinishedAt: 101,
	}).Error)

	newRate := 1.5
	notified := false
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamCollectorsAdapter{
				rates: func(context.Context, *model.UpstreamSource) (UpstreamRateGroupSnapshot, error) {
					return UpstreamRateGroupSnapshot{Groups: []UpstreamGroup{{
						ID: "premium", Name: "Premium", Status: "enabled", RateMultiplier: &newRate, EffectiveRateMultiplier: &newRate,
					}}}, nil
				},
			}, nil
		},
		Now: func() int64 { return 2000 },
		NotifyScan: func(ctx context.Context, persistedSource *model.UpstreamSource, scanID int) error {
			var changeCount int64
			require.NoError(t, model.DB.Model(&model.UpstreamSourceGroupChange{}).Where("source_id = ? AND scan_id = ?", persistedSource.Id, scanID).Count(&changeCount).Error)
			assert.Equal(t, int64(1), changeCount)
			notified = true
			return nil
		},
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.True(t, notified)
}

func TestRunDueUpstreamSourceMonitorsOnceRunsRetentionCleanup(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	t.Setenv("UPSTREAM_SOURCE_SCAN_RETENTION_DAYS", "1")
	upstreamSourceMonitorRunning.Store(false)
	upstreamSourceRetentionLastCleanupAt.Store(0)
	t.Cleanup(func() {
		upstreamSourceMonitorRunning.Store(false)
		upstreamSourceRetentionLastCleanupAt.Store(0)
	})
	source := model.UpstreamSource{
		Name: "retention-worker", Type: model.UpstreamSourceTypeSub2API,
		Status: model.UpstreamSourceStatusDisabled, BaseURL: "https://retention-worker.example.com",
	}
	require.NoError(t, model.DB.Create(&source).Error)
	oldScan := model.UpstreamSourceScan{
		SourceID: source.Id, ScanType: model.UpstreamSourceScanTypeMonitor,
		Status: model.UpstreamSourceScanStatusSuccess, StartedAt: 1, FinishedAt: 2,
	}
	require.NoError(t, model.DB.Create(&oldScan).Error)

	runDueUpstreamSourceMonitorsOnce()

	var scanCount int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceScan{}).Where("id = ?", oldScan.Id).Count(&scanCount).Error)
	assert.Zero(t, scanCount)
	assert.Greater(t, upstreamSourceRetentionLastCleanupAt.Load(), int64(0))
}

func TestUpstreamSourceMonitorRunnerAuthFailureUpdatesHealthAndSkipsSharedAuthCollectors(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "auth-failure",
		Type:                   model.UpstreamSourceTypeNewAPI,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://auth.example.com",
		AuthConfig:             `{"access_token":"stored-secret","user_id":1}`,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	var costCalls atomic.Int32
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamCollectorsAdapter{
				balance: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
					return UpstreamBalanceSnapshot{}, errors.New("upstream failed with status 401: access_token=collector-secret")
				},
				cost: func(context.Context, *model.UpstreamSource) (UpstreamCostSnapshot, error) {
					costCalls.Add(1)
					return UpstreamCostSnapshot{Amount: 1, Currency: "USD"}, nil
				},
			}, nil
		},
		Now: func() int64 { return 2000 },
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
	assert.Equal(t, 1, results[0].Failed)
	assert.Equal(t, 4, results[0].Skipped)
	assert.Zero(t, costCalls.Load())
	assert.NotContains(t, results[0].Error, "collector-secret")

	var session model.UpstreamSourceSession
	require.NoError(t, model.DB.First(&session, "source_id = ?", source.Id).Error)
	assert.Equal(t, model.UpstreamSourceAuthStatusExpired, session.AuthStatus)
	assert.NotContains(t, session.LastAuthError, "collector-secret")
	var outcomes []model.UpstreamSourceCapabilityOutcome
	require.NoError(t, model.DB.Where("scan_id = ?", results[0].ScanID).Order("id").Find(&outcomes).Error)
	require.Len(t, outcomes, 5)
	assert.Equal(t, model.UpstreamSourceCapabilityStatusFailed, outcomes[0].Status)
	assert.NotContains(t, outcomes[0].ErrorSummary, "collector-secret")
	for _, outcome := range outcomes[1:] {
		assert.Equal(t, model.UpstreamSourceCapabilityStatusSkipped, outcome.Status)
	}
}

func TestUpstreamSourceMonitorRunnerParksCredentialDecryptFailureUntilRepair(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	oldStable := common.CryptoSecretStable
	oldSecret := common.CryptoSecret
	common.CryptoSecretStable = true
	common.CryptoSecret = "upstream-source-monitor-decrypt-test"
	t.Cleanup(func() {
		common.CryptoSecretStable = oldStable
		common.CryptoSecret = oldSecret
	})

	rate := 0.0625
	source := model.UpstreamSource{
		Name:                   "decrypt-failure",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://decrypt.example.com",
		AuthConfig:             corruptedUpstreamSourceSecretEnvelope,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	channel := model.Channel{Name: "last-good / 0.0625x"}
	require.NoError(t, model.DB.Create(&channel).Error)
	mapping := model.UpstreamSourceChannelMapping{
		SourceID:                source.Id,
		UpstreamGroupID:         "last-good",
		UpstreamGroupName:       "Last Good",
		DiscoveryStatus:         model.UpstreamMappingDiscoveryStatusActive,
		UpstreamRateMultiplier:  &rate,
		EffectiveRateMultiplier: &rate,
		LocalChannelID:          channel.Id,
	}
	require.NoError(t, model.DB.Create(&mapping).Error)
	runner := UpstreamSourceMonitorRunner{Now: func() int64 { return 2000 }}

	results := runner.RunDue(context.Background(), 1000)

	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
	assert.Equal(t, 1, results[0].Failed)
	assert.Equal(t, 4, results[0].Skipped)
	assert.Equal(t, "upstream source credential decryption failed", results[0].Error)
	assert.NotContains(t, results[0].Error, "ciphertext")

	var reloadedSource model.UpstreamSource
	require.NoError(t, model.DB.First(&reloadedSource, source.Id).Error)
	assert.True(t, reloadedSource.MonitorEnabled)
	assert.Equal(t, model.UpstreamSourceMonitorParkedSchedule, reloadedSource.NextMonitorAt)
	assert.Empty(t, reloadedSource.CurrentMonitorToken)
	assert.Equal(t, model.UpstreamSourceMonitorParkedReasonCredentialDecryption, reloadedSource.MonitorParkedReason)

	var reloadedMapping model.UpstreamSourceChannelMapping
	require.NoError(t, model.DB.First(&reloadedMapping, mapping.Id).Error)
	require.NotNil(t, reloadedMapping.EffectiveRateMultiplier)
	assert.Equal(t, rate, *reloadedMapping.EffectiveRateMultiplier)
	var reloadedChannel model.Channel
	require.NoError(t, model.DB.First(&reloadedChannel, channel.Id).Error)
	assert.Equal(t, channel.Name, reloadedChannel.Name)

	var session model.UpstreamSourceSession
	require.NoError(t, model.DB.First(&session, "source_id = ?", source.Id).Error)
	assert.Equal(t, model.UpstreamSourceAuthStatusFailed, session.AuthStatus)
	assert.Equal(t, "upstream source credential decryption failed", session.LastAuthError)
	var outcomes []model.UpstreamSourceCapabilityOutcome
	require.NoError(t, model.DB.Where("scan_id = ?", results[0].ScanID).Order("id").Find(&outcomes).Error)
	require.Len(t, outcomes, 5)
	assert.Equal(t, model.UpstreamSourceCapabilityStatusFailed, outcomes[0].Status)
	assert.Equal(t, "upstream source credential decryption failed", outcomes[0].ErrorSummary)
	for _, outcome := range outcomes[1:] {
		assert.Equal(t, model.UpstreamSourceCapabilityStatusSkipped, outcome.Status)
	}

	assert.Empty(t, runner.RunDue(context.Background(), 3000), "a parked monitor must not repeat credential-decryption work")
}

func TestUpstreamSourceMonitorRunnerParksDedicatedSessionDecryptFailure(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	oldStable := common.CryptoSecretStable
	oldSecret := common.CryptoSecret
	common.CryptoSecretStable = true
	common.CryptoSecret = "upstream-source-monitor-dedicated-decrypt-test"
	t.Cleanup(func() {
		common.CryptoSecretStable = oldStable
		common.CryptoSecret = oldSecret
	})

	source := model.UpstreamSource{
		Name:                   "dedicated-decrypt-failure",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://dedicated-decrypt.example.com",
		AuthConfig:             `{"email":"owner@example.com","password":"credential"}`,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: corruptedUpstreamSourceSecretEnvelope,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))
	lastGood := model.UpstreamSourceBalanceSnapshot{
		SourceID:    source.Id,
		ScanID:      77,
		Available:   42,
		Currency:    "USD",
		CollectedAt: 900,
	}
	require.NoError(t, model.DB.Create(&lastGood).Error)
	var collectorCalls atomic.Int32
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				collectorCalls.Add(1)
				return UpstreamBalanceSnapshot{}, nil
			}}, nil
		},
		Now: func() int64 { return 2000 },
	}

	results := runner.RunDue(context.Background(), 1000)

	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
	assert.Equal(t, 1, results[0].Failed)
	assert.Equal(t, 4, results[0].Skipped)
	assert.Equal(t, "upstream source credential decryption failed", results[0].Error)
	assert.Zero(t, collectorCalls.Load())
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, model.UpstreamSourceMonitorParkedSchedule, reloaded.NextMonitorAt)
	assert.Empty(t, reloaded.CurrentMonitorToken)
	assert.Equal(t, model.UpstreamSourceMonitorParkedReasonCredentialDecryption, reloaded.MonitorParkedReason)
	var persistedLastGood model.UpstreamSourceBalanceSnapshot
	require.NoError(t, model.DB.First(&persistedLastGood, "source_id = ?", source.Id).Error)
	assert.Equal(t, lastGood.ScanID, persistedLastGood.ScanID)
	assert.Equal(t, lastGood.Available, persistedLastGood.Available)
	assert.Equal(t, lastGood.CollectedAt, persistedLastGood.CollectedAt)
	var outcomes []model.UpstreamSourceCapabilityOutcome
	require.NoError(t, model.DB.Where("scan_id = ?", results[0].ScanID).Order("id").Find(&outcomes).Error)
	require.Len(t, outcomes, 5)
	assert.Equal(t, model.UpstreamSourceCapabilityStatusFailed, outcomes[0].Status)
	assert.Equal(t, "upstream source credential decryption failed", outcomes[0].ErrorSummary)
	for _, outcome := range outcomes[1:] {
		assert.Equal(t, model.UpstreamSourceCapabilityStatusSkipped, outcome.Status)
		assert.Equal(t, "skipped after authentication failure", outcome.ErrorSummary)
	}
}

func TestUpstreamSourceMonitorRunnerLaterAuthFailurePreservesFailedHealth(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "later-auth-failure",
		Type:                   model.UpstreamSourceTypeNewAPI,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://auth.example.com",
		AuthConfig:             `{"access_token":"stored-secret","user_id":1}`,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamCollectorsAdapter{
				balance: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
					return UpstreamBalanceSnapshot{Available: 5, Currency: "quota"}, nil
				},
				cost: func(context.Context, *model.UpstreamSource) (UpstreamCostSnapshot, error) {
					return UpstreamCostSnapshot{}, errors.New("upstream failed with status 401: access_token=collector-secret")
				},
			}, nil
		},
		Now: func() int64 { return 2000 },
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusPartial, results[0].Status)
	assert.Equal(t, 1, results[0].Collected)
	assert.Equal(t, 1, results[0].Failed)
	assert.Equal(t, 3, results[0].Skipped)

	var session model.UpstreamSourceSession
	require.NoError(t, model.DB.First(&session, "source_id = ?", source.Id).Error)
	assert.Equal(t, model.UpstreamSourceAuthStatusExpired, session.AuthStatus)
	assert.NotContains(t, session.LastAuthError, "collector-secret")
}

func TestUpstreamSourceMonitorRunnerNonAuthFailureDoesNotPoisonAuth(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "business-failure",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://business.example.com",
		AuthConfig:             `{}`,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamCollectorsAdapter{
				balance: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
					return UpstreamBalanceSnapshot{}, errors.New("temporary upstream business failure api_key=collector-secret")
				},
				cost: func(context.Context, *model.UpstreamSource) (UpstreamCostSnapshot, error) {
					return UpstreamCostSnapshot{Amount: 2.5, Currency: "USD", PeriodStart: 1, PeriodEnd: 2}, nil
				},
			}, nil
		},
		Now: func() int64 { return 2000 },
	}

	results := runner.RunDue(context.Background(), 1000)
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusPartial, results[0].Status)
	assert.Equal(t, 1, results[0].Failed)
	assert.Equal(t, 4, results[0].Collected)
	assert.Zero(t, results[0].Skipped)
	assert.NotContains(t, results[0].Error, "collector-secret")
	var session model.UpstreamSourceSession
	require.NoError(t, model.DB.First(&session, "source_id = ?", source.Id).Error)
	assert.Equal(t, model.UpstreamSourceAuthStatusHealthy, session.AuthStatus)
	var cost model.UpstreamSourceCostSnapshot
	require.NoError(t, model.DB.First(&cost, "source_id = ?", source.Id).Error)
	assert.Equal(t, 2.5, cost.Amount)
}

func TestUpstreamSourceMonitorRunnerClaimIsolationWithCollectorsRegistered(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "runner-claim",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://claim.example.com",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	factory := func(string) (UpstreamSourceAdapter, error) {
		return fakeUpstreamBalanceAdapter{collect: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
			calls.Add(1)
			started <- struct{}{}
			<-release
			return UpstreamBalanceSnapshot{Available: 1, Currency: "USD"}, nil
		}}, nil
	}
	runnerA := UpstreamSourceMonitorRunner{AdapterFactory: factory, Now: func() int64 { return 2000 }}
	runnerB := UpstreamSourceMonitorRunner{AdapterFactory: factory, Now: func() int64 { return 2000 }}
	done := make(chan []UpstreamSourceMonitorResult, 1)
	go func() { done <- runnerA.RunDue(context.Background(), 1000) }()
	<-started

	secondResults := runnerB.RunDue(context.Background(), 1000)
	close(release)
	firstResults := <-done

	require.Len(t, firstResults, 1)
	assert.Empty(t, secondResults)
	assert.Equal(t, int32(1), calls.Load())
	var scans int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceScan{}).Where("source_id = ? AND scan_type = ?", source.Id, model.UpstreamSourceScanTypeMonitor).Count(&scans).Error)
	assert.Equal(t, int64(1), scans)
	var outcomes int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceCapabilityOutcome{}).Where("source_id = ?", source.Id).Count(&outcomes).Error)
	assert.Equal(t, int64(5), outcomes)
}

func TestUpstreamSourceMonitorCredentialRepairInvalidatesStaleParkRelease(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "repair-release-race",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://repair.example.com",
		AuthConfig:             `{}`,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:   source.Id,
		AuthStatus: model.UpstreamSourceAuthStatusHealthy,
	}))

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				started <- struct{}{}
				<-release
				return UpstreamBalanceSnapshot{}, ErrUpstreamSourceCredentialDecryption
			}}, nil
		},
		Now: func() int64 { return 1000 },
	}
	done := make(chan []UpstreamSourceMonitorResult, 1)
	go func() { done <- runner.RunDue(context.Background(), 1000) }()
	<-started

	repairedConfig, err := WriteUpstreamSourceAuthConfig(`{"email":"repaired@example.com","password":"replacement"}`)
	require.NoError(t, err)
	repaired, err := UpdateUpstreamSourceCredentials(source.Id, repairedConfig, 1000)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), repaired.NextMonitorAt)
	assert.NotEmpty(t, repaired.CurrentMonitorToken)

	close(release)
	results := <-done
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, int64(1000), reloaded.NextMonitorAt)
	assert.Empty(t, reloaded.CurrentMonitorToken)
	plaintext, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	var persisted sub2APIAuthConfig
	require.NoError(t, common.UnmarshalJsonStr(plaintext, &persisted))
	assert.True(t, persisted.Email == "repaired@example.com", "repaired credentials must remain authoritative")
	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	assert.False(t, session != nil, "the stale runner must not recreate auth health cleared by credential repair")
}

func TestUpstreamSourceMonitorCredentialRepairPreservesActiveClaim(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "repair-active-claim",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://repair-active.example.com",
		AuthConfig:             `{}`,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)

	started := make(chan int32, 2)
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	factory := func(string) (UpstreamSourceAdapter, error) {
		return fakeUpstreamBalanceAdapter{collect: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
			call := calls.Add(1)
			started <- call
			if call == 1 {
				<-releaseFirst
			}
			return UpstreamBalanceSnapshot{Available: 1, Currency: "USD"}, nil
		}}, nil
	}
	runnerA := UpstreamSourceMonitorRunner{AdapterFactory: factory, Now: func() int64 { return 1000 }}
	runnerB := UpstreamSourceMonitorRunner{AdapterFactory: factory, Now: func() int64 { return 1000 }}
	done := make(chan []UpstreamSourceMonitorResult, 1)
	go func() { done <- runnerA.RunDue(context.Background(), 1000) }()
	assert.Equal(t, int32(1), <-started)

	repairedConfig, err := WriteUpstreamSourceAuthConfig(`{"email":"repaired@example.com","password":"replacement"}`)
	require.NoError(t, err)
	repaired, err := UpdateUpstreamSourceCredentials(source.Id, repairedConfig, 1000)
	require.NoError(t, err)
	secondResults := runnerB.RunDue(context.Background(), 1000)

	assert.Empty(t, secondResults)
	assert.Equal(t, int32(1), calls.Load())
	assert.NotEmpty(t, repaired.CurrentMonitorToken)
	var scans int64
	require.NoError(t, model.DB.Model(&model.UpstreamSourceScan{}).Where("source_id = ? AND scan_type = ?", source.Id, model.UpstreamSourceScanTypeMonitor).Count(&scans).Error)
	assert.Equal(t, int64(1), scans)

	close(releaseFirst)
	results := <-done
	require.Len(t, results, 1)
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	plaintext, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	var persisted sub2APIAuthConfig
	require.NoError(t, common.UnmarshalJsonStr(plaintext, &persisted))
	assert.True(t, persisted.Email == "repaired@example.com", "repaired credentials must remain authoritative")
	assert.Empty(t, reloaded.CurrentMonitorToken)
}

func TestUpstreamSourceMonitorSessionClearWinsStaleSuccessfulPersistence(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:                   "clear-session-active-run",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://clear-session.example.com",
		AuthConfig:             `{"email":"owner@example.com","password":"credential"}`,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"dedicated-session","refresh_token":"dedicated-refresh","expires_at":9999}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				started <- struct{}{}
				<-release
				return UpstreamBalanceSnapshot{Available: 1, Currency: "USD"}, nil
			}}, nil
		},
		Now: func() int64 { return 1000 },
	}
	done := make(chan []UpstreamSourceMonitorResult, 1)
	go func() { done <- runner.RunDue(context.Background(), 1000) }()
	<-started

	require.NoError(t, ClearUpstreamSourceSession(source.Id))
	close(release)
	results := <-done
	require.Len(t, results, 1)

	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	assert.False(t, session != nil, "stale monitor persistence must not recreate a cleared session")
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	plaintext, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	var persisted sub2APIAuthConfig
	require.NoError(t, common.UnmarshalJsonStr(plaintext, &persisted))
	assert.False(t, persisted.AccessToken != "" || persisted.RefreshToken != "", "cleared session material must remain absent")
}

func TestUpstreamSourceMonitorChildPanicBoundaryCoversPreClaimSeams(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	require.NoError(t, model.DB.Create(&model.UpstreamSource{
		Name:                   "monitor-pre-claim-panic",
		Type:                   model.UpstreamSourceTypeSub2API,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                "https://monitor-panic.example",
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1_000,
	}).Error)

	runner := UpstreamSourceMonitorRunner{
		NewToken: func() string { panic("injected pre-claim seam panic") },
		Now:      func() int64 { return 1_000 },
	}
	require.NotPanics(t, func() {
		results := runner.RunDue(context.Background(), 1_000)
		require.Len(t, results, 1)
		assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
		assert.NotEmpty(t, results[0].Error)
	})
}

func TestUpstreamSourceMonitorWorkerContinuesAfterPreClaimPanic(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	sources := []model.UpstreamSource{
		{Name: "panics-before-claim", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://first.example", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1_000},
		{Name: "runs-after-panic", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://second.example", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1_000},
	}
	require.NoError(t, model.DB.Create(&sources).Error)
	var tokenCalls atomic.Int64
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) { return fakeUpstreamSourceAdapter{}, nil },
		NewToken: func() string {
			if tokenCalls.Add(1) == 1 {
				panic("injected first-job panic")
			}
			return "second-job-token"
		},
		Now:            func() int64 { return 1_000 },
		MaxConcurrency: 1,
	}

	results := runner.RunDue(context.Background(), 1_000)
	require.Len(t, results, 2)
	assert.Equal(t, sources[0].Id, results[0].SourceID)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
	assert.Equal(t, sources[1].Id, results[1].SourceID)
	assert.Equal(t, model.UpstreamSourceScanStatusSuccess, results[1].Status)
}

func TestUpstreamSourceMonitorChildPanicBoundaryCoversClaimAndScanConstruction(t *testing.T) {
	tests := []struct {
		name             string
		registerCallback func(t *testing.T, panicked *atomic.Bool)
	}{
		{name: "claim", registerCallback: func(t *testing.T, panicked *atomic.Bool) {
			const callbackName = "test:monitor_claim_panic"
			require.NoError(t, model.DB.Callback().Update().After("gorm:update").Register(callbackName, func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]interface{})
				if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "UpstreamSource" || !ok {
					return
				}
				if token, claiming := values["current_monitor_token"].(string); claiming && token != "" && panicked.CompareAndSwap(false, true) {
					panic("injected monitor claim panic")
				}
			}))
			t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(callbackName) })
		}},
		{name: "scan construction", registerCallback: func(t *testing.T, panicked *atomic.Bool) {
			const callbackName = "test:monitor_scan_create_panic"
			require.NoError(t, model.DB.Callback().Create().After("gorm:create").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "UpstreamSourceScan" && panicked.CompareAndSwap(false, true) {
					panic("injected monitor scan construction panic")
				}
			}))
			t.Cleanup(func() { _ = model.DB.Callback().Create().Remove(callbackName) })
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupUpstreamSourceServiceTestDB(t)
			source := model.UpstreamSource{
				Name: "monitor-seam-panic", Type: model.UpstreamSourceTypeSub2API,
				Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://seam-panic.example",
				MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1_000,
			}
			require.NoError(t, model.DB.Create(&source).Error)
			var panicked atomic.Bool
			tt.registerCallback(t, &panicked)
			runner := UpstreamSourceMonitorRunner{
				AdapterFactory: func(string) (UpstreamSourceAdapter, error) { return fakeUpstreamSourceAdapter{}, nil },
				NewToken:       func() string { return "seam-panic-token" },
				Now:            func() int64 { return 1_000 },
				MaxConcurrency: 1,
			}

			results := runner.RunDue(context.Background(), 1_000)
			require.True(t, panicked.Load())
			require.Len(t, results, 1)
			assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
			assert.NotEmpty(t, results[0].Error)
			var reloaded model.UpstreamSource
			require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
			assert.Empty(t, reloaded.CurrentMonitorToken)
			assert.Zero(t, reloaded.MonitorStartedAt)
			var running int64
			require.NoError(t, model.DB.Model(&model.UpstreamSourceScan{}).
				Where("source_id = ? AND status = ?", source.Id, model.UpstreamSourceScanStatusRunning).
				Count(&running).Error)
			assert.Zero(t, running)
		})
	}
}

func TestUpstreamSourceMonitorFinalizesClaimAfterCollectorPanic(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name: "monitor-collector-panic", Type: model.UpstreamSourceTypeSub2API,
		Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://collector-panic.example",
		MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1_000,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				panic("injected collector panic")
			}}, nil
		},
		NewToken: func() string { return "collector-panic-token" },
		Now:      func() int64 { return 1_000 },
	}

	results := runner.RunDue(context.Background(), 1_000)
	require.Len(t, results, 1)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, results[0].Status)
	assert.NotEmpty(t, results[0].Error)
	var scan model.UpstreamSourceScan
	require.NoError(t, model.DB.First(&scan, results[0].ScanID).Error)
	assert.Equal(t, model.UpstreamSourceScanStatusFailed, scan.Status)
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Empty(t, reloaded.CurrentMonitorToken)
	assert.Zero(t, reloaded.MonitorStartedAt)
}
