package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{Name: "due", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://two.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 900},
		{Name: "future", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://three.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 1001},
		{Name: "disabled-source", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusDisabled, BaseURL: "https://four.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10, NextMonitorAt: 0},
	}
	require.NoError(t, model.DB.Create(&sources).Error)

	due, err := model.ListDueUpstreamSourcesForMonitor(1000, 100)
	require.NoError(t, err)
	require.Len(t, due, 1)
	assert.Equal(t, sources[1].Id, due[0].Id)
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
		NextMonitorAt:          0,
	}
	require.NoError(t, model.DB.Create(&source).Error)

	claimedA, err := model.ClaimUpstreamSourceMonitor(source.Id, "worker-a", 1000, 60)
	require.NoError(t, err)
	claimedB, err := model.ClaimUpstreamSourceMonitor(source.Id, "worker-b", 1000, 60)
	require.NoError(t, err)

	assert.True(t, claimedA)
	assert.False(t, claimedB)
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
	}
	require.NoError(t, model.DB.Create(&source).Error)
	claimed, err := model.ClaimUpstreamSourceMonitor(source.Id, "worker", 1000, 60)
	require.NoError(t, err)
	require.True(t, claimed)
	_, err = UpdateUpstreamSourceMonitorSettings(source.Id, false, 10, 1100)
	require.NoError(t, err)

	require.NoError(t, model.ReleaseUpstreamSourceMonitor(source.Id, "worker", 1200))

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.False(t, reloaded.MonitorEnabled)
	assert.Zero(t, reloaded.NextMonitorAt)
	assert.Empty(t, reloaded.CurrentMonitorToken)
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

func TestUpstreamSourceMonitorRunnerBoundsConcurrency(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	sources := []model.UpstreamSource{
		{Name: "one", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://one.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10},
		{Name: "two", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://two.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10},
		{Name: "three", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://three.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10},
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
		{Name: "success-one", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://one.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10},
		{Name: "fails", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://two.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10},
		{Name: "success-two", Type: model.UpstreamSourceTypeSub2API, Status: model.UpstreamSourceStatusEnabled, BaseURL: "https://three.example.com", MonitorEnabled: true, MonitorIntervalMinutes: 10},
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
