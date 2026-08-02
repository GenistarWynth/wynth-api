package model

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaydto "github.com/QuantumNous/new-api/relaykit/dto"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const (
	DefaultUpstreamSourceBillingProbeIntervalMinutes = 30
	MinUpstreamSourceBillingProbeIntervalMinutes     = 5
	MaxUpstreamSourceBillingProbeIntervalMinutes     = 24 * 60

	UpstreamSourceBillingProbeStatusIdle        = "idle"
	UpstreamSourceBillingProbeStatusOK          = "ok"
	UpstreamSourceBillingProbeStatusFailed      = "failed"
	UpstreamSourceBillingProbeStatusUnsupported = "unsupported"
)

const upstreamSourceBillingProbeEligibleSQLTemplate = `EXISTS (
	SELECT 1
	FROM upstream_source_channel_mappings AS billing_probe_mapping
	JOIN upstream_sources AS billing_probe_source ON billing_probe_source.id = billing_probe_mapping.source_id
	JOIN channels AS billing_probe_channel ON billing_probe_channel.id = billing_probe_mapping.local_channel_id
	WHERE billing_probe_mapping.id = upstream_source_billing_probes.mapping_id
		AND billing_probe_mapping.source_id = upstream_source_billing_probes.source_id
		AND billing_probe_source.type = ?
		AND billing_probe_source.status = ?
		AND billing_probe_mapping.sync_enabled = ?
		AND billing_probe_mapping.discovery_status = ?
		AND billing_probe_mapping.upstream_key_id <> ?
		AND billing_probe_channel.status = ?
		AND billing_probe_channel.type > ?
		AND TRIM(billing_probe_channel.%s) <> ?
		AND TRIM(billing_probe_channel.settings) <> ?
)`

type upstreamSourceBillingProbeEligibility struct {
	Probe   UpstreamSourceBillingProbe
	Source  UpstreamSource
	Mapping UpstreamSourceChannelMapping
	Channel Channel
}

type upstreamSourceBillingProbeID int

type legacyUpstreamSourceBillingProbeIdentity struct {
	Id        int
	MappingID *int
}

func (legacyUpstreamSourceBillingProbeIdentity) TableName() string {
	return "upstream_source_billing_probes"
}

// GormDBDataType is the model-level seam GORM invokes before comparing
// existing columns during a direct AutoMigrate. Keep the guard here as well as
// at the application migration boundary so legacy duplicates are repaired
// before GORM attempts to add the mapping uniqueness constraint. An empty
// result delegates primary-key typing and generation to GORM's dialect.
func (upstreamSourceBillingProbeID) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if err := deduplicateLegacyUpstreamSourceBillingProbes(db); err != nil {
		common.SysError("failed to prepare legacy upstream billing probes for migration: " + err.Error())
		return "billing_probe_migration_failed("
	}
	return ""
}

// UpstreamSourceBillingProbe is the durable, secret-free observation state for
// one generated upstream-source mapping. Bearer material only exists in the
// channel row and is never copied here.
type UpstreamSourceBillingProbe struct {
	Id        upstreamSourceBillingProbeID `json:"id"`
	SourceID  int                          `json:"source_id" gorm:"index"`
	MappingID int                          `json:"mapping_id" gorm:"uniqueIndex:idx_upstream_source_billing_probe_mapping_id"`

	Enabled         bool   `json:"enabled" gorm:"index"`
	IntervalMinutes int    `json:"interval_minutes"`
	Status          string `json:"status" gorm:"type:varchar(32);index"`
	ErrorCode       string `json:"error_code" gorm:"type:varchar(64)"`
	HTTPStatus      int    `json:"http_status"`
	FailureCount    int    `json:"failure_count"`

	GroupRateMultiplier     *float64 `json:"group_rate_multiplier,omitempty"`
	UserRateMultiplier      *float64 `json:"user_rate_multiplier,omitempty"`
	ResolvedRateMultiplier  *float64 `json:"resolved_rate_multiplier,omitempty"`
	PeakRateEnabled         *bool    `json:"peak_rate_enabled,omitempty"`
	PeakStart               string   `json:"peak_start,omitempty" gorm:"type:varchar(5)"`
	PeakEnd                 string   `json:"peak_end,omitempty" gorm:"type:varchar(5)"`
	PeakRateMultiplier      *float64 `json:"peak_rate_multiplier,omitempty"`
	AppliedPeakMultiplier   *float64 `json:"applied_peak_multiplier,omitempty"`
	EffectiveRateMultiplier *float64 `json:"effective_rate_multiplier,omitempty"`
	Timezone                string   `json:"timezone,omitempty" gorm:"type:varchar(191)"`
	ObservedAt              string   `json:"observed_at,omitempty" gorm:"type:varchar(64)"`

	LastAttemptAt int64 `json:"last_attempt_at" gorm:"bigint"`
	ReceivedAt    int64 `json:"received_at" gorm:"bigint"`
	FreshUntil    int64 `json:"fresh_until" gorm:"bigint;index"`
	NextProbeAt   int64 `json:"next_probe_at" gorm:"bigint;index"`
	// LastGoodGeneration advances only when a sanitized observation is accepted.
	// Latest attempts and retained failures never advance it.
	LastGoodGeneration int64 `json:"last_good_generation" gorm:"bigint"`

	LastGoodIdentityFingerprint string `json:"-" gorm:"type:varchar(64)"`
	ClaimIdentityFingerprint    string `json:"-" gorm:"type:varchar(64)"`
	LeaseToken                  string `json:"-" gorm:"type:varchar(64);index"`
	LeaseStartedAt              int64  `json:"-" gorm:"bigint;index"`
	CreatedTime                 int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime                 int64  `json:"updated_time" gorm:"bigint"`
}

func deduplicateLegacyUpstreamSourceBillingProbes(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if !db.Migrator().HasTable("upstream_source_billing_probes") {
		return nil
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var rows []legacyUpstreamSourceBillingProbeIdentity
		if err := tx.
			Select("id", "mapping_id").
			Where("mapping_id IS NOT NULL").
			Order("mapping_id, id").
			Find(&rows).Error; err != nil {
			return err
		}

		loserIDs := make([]int, 0)
		var previousMappingID int
		havePrevious := false
		for _, row := range rows {
			if row.MappingID == nil {
				continue
			}
			if havePrevious && *row.MappingID == previousMappingID {
				loserIDs = append(loserIDs, row.Id)
				continue
			}
			previousMappingID = *row.MappingID
			havePrevious = true
		}

		const deleteBatchSize = 500
		for start := 0; start < len(loserIDs); start += deleteBatchSize {
			end := start + deleteBatchSize
			if end > len(loserIDs) {
				end = len(loserIDs)
			}
			batch := loserIDs[start:end]
			result := tx.Where("id IN ?", batch).Delete(&legacyUpstreamSourceBillingProbeIdentity{})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != int64(len(batch)) {
				return errors.New("legacy upstream billing probe deduplication affected an unexpected number of rows")
			}
		}
		return nil
	})
}

type UpstreamSourceBillingProbeIdentityRows struct {
	Probe   UpstreamSourceBillingProbe
	Source  UpstreamSource
	Mapping UpstreamSourceChannelMapping
	Channel Channel
}

func (probe *UpstreamSourceBillingProbe) BeforeCreate(*gorm.DB) error {
	if probe.SourceID == 0 || probe.MappingID == 0 {
		return errors.New("source and mapping IDs are required")
	}
	probe.IntervalMinutes = NormalizeUpstreamSourceBillingProbeInterval(probe.IntervalMinutes)
	if probe.Status == "" {
		probe.Status = UpstreamSourceBillingProbeStatusIdle
	}
	now := common.GetTimestamp()
	if probe.CreatedTime == 0 {
		probe.CreatedTime = now
	}
	if probe.UpdatedTime == 0 {
		probe.UpdatedTime = now
	}
	return nil
}

func NormalizeUpstreamSourceBillingProbeInterval(intervalMinutes int) int {
	if intervalMinutes < MinUpstreamSourceBillingProbeIntervalMinutes || intervalMinutes > MaxUpstreamSourceBillingProbeIntervalMinutes {
		return DefaultUpstreamSourceBillingProbeIntervalMinutes
	}
	return intervalMinutes
}

func validUpstreamSourceBillingProbeStatus(status string) bool {
	switch status {
	case UpstreamSourceBillingProbeStatusIdle,
		UpstreamSourceBillingProbeStatusOK,
		UpstreamSourceBillingProbeStatusFailed,
		UpstreamSourceBillingProbeStatusUnsupported:
		return true
	default:
		return false
	}
}

func backfillUpstreamSourceBillingProbeDefaults() error {
	return DB.Transaction(func(tx *gorm.DB) error {
		updates := []struct {
			where  string
			column string
			value  any
		}{
			{where: "enabled IS NULL", column: "enabled", value: false},
			{where: "interval_minutes IS NULL OR interval_minutes < ? OR interval_minutes > ?", column: "interval_minutes", value: DefaultUpstreamSourceBillingProbeIntervalMinutes},
			{where: "status IS NULL OR status = ?", column: "status", value: UpstreamSourceBillingProbeStatusIdle},
			{where: "lease_token IS NULL", column: "lease_token", value: ""},
			{where: "lease_started_at IS NULL", column: "lease_started_at", value: int64(0)},
			{where: "last_good_identity_fingerprint IS NULL", column: "last_good_identity_fingerprint", value: ""},
			{where: "claim_identity_fingerprint IS NULL", column: "claim_identity_fingerprint", value: ""},
		}
		for _, update := range updates {
			query := tx.Model(&UpstreamSourceBillingProbe{})
			switch update.column {
			case "interval_minutes":
				query = query.Where(update.where, MinUpstreamSourceBillingProbeIntervalMinutes, MaxUpstreamSourceBillingProbeIntervalMinutes)
			case "status":
				query = query.Where(update.where, "")
			default:
				query = query.Where(update.where)
			}
			if err := query.UpdateColumn(update.column, update.value).Error; err != nil {
				return err
			}
		}

		return tx.Model(&UpstreamSourceBillingProbe{}).
			Where("status NOT IN ?", []string{
				UpstreamSourceBillingProbeStatusIdle,
				UpstreamSourceBillingProbeStatusOK,
				UpstreamSourceBillingProbeStatusFailed,
				UpstreamSourceBillingProbeStatusUnsupported,
			}).
			UpdateColumn("status", UpstreamSourceBillingProbeStatusIdle).Error
	})
}

func eligibleUpstreamSourceBillingProbeQuery(db *gorm.DB, expectedChannel ...*Channel) *gorm.DB {
	eligibleSQL := fmt.Sprintf(upstreamSourceBillingProbeEligibleSQLTemplate, commonKeyCol)
	args := []any{
		UpstreamSourceTypeSub2API,
		UpstreamSourceStatusEnabled,
		true,
		UpstreamMappingDiscoveryStatusActive,
		"",
		common.ChannelStatusEnabled,
		constant.ChannelTypeUnknown,
		"",
		"",
	}
	if len(expectedChannel) > 0 && expectedChannel[0] != nil {
		singleKeyPredicate := "COALESCE(json_extract(billing_probe_channel.channel_info, '$.is_multi_key'), 0) = 0"
		if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
			singleKeyPredicate = "COALESCE(billing_probe_channel.channel_info ->> 'is_multi_key', 'false') = 'false'"
		} else if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
			singleKeyPredicate = "COALESCE(JSON_UNQUOTE(JSON_EXTRACT(billing_probe_channel.channel_info, '$.is_multi_key')), 'false') = 'false'"
		}
		eligibleSQL = strings.TrimSuffix(eligibleSQL, ")") + fmt.Sprintf(`
		AND billing_probe_channel.id = ?
		AND billing_probe_channel.settings = ?
		AND billing_probe_channel.%s = ?
		AND billing_probe_channel.type = ?
		AND %s
)`, commonKeyCol, singleKeyPredicate)
		args = append(args,
			expectedChannel[0].Id,
			expectedChannel[0].OtherSettings,
			expectedChannel[0].Key,
			expectedChannel[0].Type,
		)
	}
	return db.Where(
		eligibleSQL,
		args...,
	)
}

func isUpstreamSourceBillingProbeLifecycleEligible(
	probe *UpstreamSourceBillingProbe,
	source *UpstreamSource,
	mapping *UpstreamSourceChannelMapping,
	channel *Channel,
) bool {
	if probe == nil || source == nil || mapping == nil || channel == nil ||
		!probe.Enabled || probe.SourceID != source.Id || probe.MappingID != mapping.Id ||
		source.Type != UpstreamSourceTypeSub2API || source.Status != UpstreamSourceStatusEnabled ||
		mapping.SourceID != source.Id || !mapping.SyncEnabled ||
		mapping.DiscoveryStatus != UpstreamMappingDiscoveryStatusActive ||
		strings.TrimSpace(mapping.UpstreamKeyID) == "" || mapping.LocalChannelID == 0 ||
		channel.Id != mapping.LocalChannelID || channel.Status != common.ChannelStatusEnabled ||
		!IsSupportedUpstreamSourceBillingProbeChannelType(channel.Type) || channel.ChannelInfo.IsMultiKey {
		return false
	}

	bearerKey := strings.TrimSpace(channel.Key)
	if bearerKey == "" || strings.ContainsAny(channel.Key, "\r\n") || strings.HasPrefix(bearerKey, "[") {
		return false
	}
	var ownership relaydto.ChannelOtherSettings
	return strings.TrimSpace(channel.OtherSettings) != "" &&
		common.UnmarshalJsonStr(channel.OtherSettings, &ownership) == nil &&
		ownership.GeneratedByUpstreamSourceID == source.Id &&
		ownership.GeneratedByUpstreamMappingID == mapping.Id
}

func IsUpstreamSourceBillingProbeIdentityRowsEligible(rows *UpstreamSourceBillingProbeIdentityRows) bool {
	return rows != nil && isUpstreamSourceBillingProbeLifecycleEligible(
		&rows.Probe,
		&rows.Source,
		&rows.Mapping,
		&rows.Channel,
	)
}

func IsSupportedUpstreamSourceBillingProbeChannelType(channelType int) bool {
	return channelType == constant.ChannelTypeOpenAI
}

func loadUpstreamSourceBillingProbeEligibility(ctx context.Context, probe UpstreamSourceBillingProbe) (*upstreamSourceBillingProbeEligibility, bool, error) {
	eligibility := &upstreamSourceBillingProbeEligibility{Probe: probe}
	if err := DB.WithContext(ctx).Where("id = ?", probe.SourceID).First(&eligibility.Source).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if err := DB.WithContext(ctx).
		Where("id = ? AND source_id = ?", probe.MappingID, probe.SourceID).
		First(&eligibility.Mapping).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if eligibility.Mapping.LocalChannelID == 0 {
		return nil, false, nil
	}
	if err := DB.WithContext(ctx).Where("id = ?", eligibility.Mapping.LocalChannelID).First(&eligibility.Channel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}

	if !isUpstreamSourceBillingProbeLifecycleEligible(
		&eligibility.Probe,
		&eligibility.Source,
		&eligibility.Mapping,
		&eligibility.Channel,
	) {
		return nil, false, nil
	}
	return eligibility, true, nil
}

func listEligibleUpstreamSourceBillingProbes(ctx context.Context, query *gorm.DB, limit int) ([]upstreamSourceBillingProbeEligibility, error) {
	if limit <= 0 {
		return []upstreamSourceBillingProbeEligibility{}, nil
	}
	pageSize := limit * 2
	if pageSize < 50 {
		pageSize = 50
	}
	if pageSize > 1000 {
		pageSize = 1000
	}
	eligible := make([]upstreamSourceBillingProbeEligibility, 0, limit)
	for offset := 0; len(eligible) < limit; {
		var probes []UpstreamSourceBillingProbe
		if err := query.Offset(offset).Limit(pageSize).Find(&probes).Error; err != nil {
			return nil, err
		}
		for _, probe := range probes {
			candidate, ok, err := loadUpstreamSourceBillingProbeEligibility(ctx, probe)
			if err != nil {
				return nil, err
			}
			if ok {
				eligible = append(eligible, *candidate)
				if len(eligible) == limit {
					break
				}
			}
		}
		if len(probes) < pageSize {
			break
		}
		offset += len(probes)
	}
	return eligible, nil
}

func DeleteUpstreamSourceWithBillingProbeCleanup(sourceID int, now int64) error {
	if sourceID == 0 {
		return errors.New("source ID is required")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockUpstreamSourceTx(tx, sourceID); err != nil {
			return err
		}
		result := tx.Model(&UpstreamSource{}).Where("id = ?", sourceID).Updates(map[string]any{
			"status":       UpstreamSourceStatusDeleted,
			"updated_time": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("upstream source delete affected an unexpected number of rows")
		}
		return tx.Where("source_id = ?", sourceID).Delete(&UpstreamSourceBillingProbe{}).Error
	})
}

func ListDueUpstreamSourceBillingProbes(now int64, limit int) ([]UpstreamSourceBillingProbe, error) {
	return ListDueUpstreamSourceBillingProbesWithContext(context.Background(), now, limit)
}

func ListDueUpstreamSourceBillingProbesWithContext(ctx context.Context, now int64, limit int) ([]UpstreamSourceBillingProbe, error) {
	if limit <= 0 {
		return []UpstreamSourceBillingProbe{}, nil
	}
	if limit > 1000 {
		limit = 1000
	}
	query := eligibleUpstreamSourceBillingProbeQuery(DB.WithContext(ctx).Model(&UpstreamSourceBillingProbe{})).
		Where("enabled = ? AND next_probe_at <= ?", true, now).
		Order("next_probe_at, mapping_id")
	eligible, err := listEligibleUpstreamSourceBillingProbes(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	probes := make([]UpstreamSourceBillingProbe, 0, len(eligible))
	for _, candidate := range eligible {
		probes = append(probes, candidate.Probe)
	}
	return probes, nil
}

func ClaimUpstreamSourceBillingProbe(mappingID int, token string, now int64, staleAfterSeconds int64, requireDue bool) (*UpstreamSourceBillingProbe, error) {
	return ClaimUpstreamSourceBillingProbeWithContext(context.Background(), mappingID, token, now, staleAfterSeconds, requireDue)
}

func ClaimUpstreamSourceBillingProbeWithContext(ctx context.Context, mappingID int, token string, now int64, staleAfterSeconds int64, requireDue bool) (*UpstreamSourceBillingProbe, error) {
	if mappingID == 0 {
		return nil, errors.New("mapping ID is required")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("billing probe lease token is required")
	}
	if staleAfterSeconds < 1 {
		staleAfterSeconds = 1
	}
	staleBefore := now - staleAfterSeconds
	query := eligibleUpstreamSourceBillingProbeQuery(DB.WithContext(ctx).Model(&UpstreamSourceBillingProbe{})).
		Where("mapping_id = ? AND enabled = ?", mappingID, true).
		Where("lease_token = ? OR lease_token IS NULL OR lease_started_at <= ?", "", staleBefore)
	if requireDue {
		query = query.Where("next_probe_at <= ?", now)
	}
	eligible, err := listEligibleUpstreamSourceBillingProbes(ctx, query, 1)
	if err != nil || len(eligible) == 0 {
		return nil, err
	}
	candidate := eligible[0]
	claimQuery := eligibleUpstreamSourceBillingProbeQuery(
		DB.WithContext(ctx).Model(&UpstreamSourceBillingProbe{}),
		&candidate.Channel,
	).Where("mapping_id = ? AND enabled = ?", mappingID, true).
		Where("lease_token = ? OR lease_token IS NULL OR lease_started_at <= ?", "", staleBefore)
	if requireDue {
		claimQuery = claimQuery.Where("next_probe_at <= ?", now)
	}
	result := claimQuery.Updates(map[string]any{
		"claim_identity_fingerprint": "",
		"lease_token":                token,
		"lease_started_at":           now,
		"updated_time":               now,
	})
	if result.Error != nil || result.RowsAffected == 0 {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, errors.New("billing probe claim affected multiple rows")
	}

	var probe UpstreamSourceBillingProbe
	if err := DB.WithContext(ctx).
		Where("mapping_id = ? AND lease_token = ?", mappingID, token).
		First(&probe).Error; err != nil {
		return nil, err
	}
	return &probe, nil
}

func ReleaseUpstreamSourceBillingProbeLease(mappingID int, token string, nextProbeAt int64, now int64) (bool, error) {
	return ReleaseUpstreamSourceBillingProbeLeaseWithContext(context.Background(), mappingID, token, nextProbeAt, now)
}

func ReleaseUpstreamSourceBillingProbeLeaseWithContext(ctx context.Context, mappingID int, token string, nextProbeAt int64, now int64) (bool, error) {
	if mappingID == 0 {
		return false, errors.New("mapping ID is required")
	}
	if strings.TrimSpace(token) == "" {
		return false, errors.New("billing probe lease token is required")
	}
	result := DB.WithContext(ctx).Model(&UpstreamSourceBillingProbe{}).
		Where("mapping_id = ? AND lease_token = ?", mappingID, token).
		Updates(map[string]any{
			"claim_identity_fingerprint": "",
			"lease_token":                "",
			"lease_started_at":           0,
			"next_probe_at":              nextProbeAt,
			"updated_time":               now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected > 1 {
		return false, errors.New("billing probe lease release affected multiple rows")
	}
	return result.RowsAffected == 1, nil
}

func LockUpstreamSourceBillingProbeTx(tx *gorm.DB, mappingID int) (*UpstreamSourceBillingProbe, error) {
	if tx == nil {
		return nil, errors.New("database transaction is required")
	}
	if mappingID == 0 {
		return nil, errors.New("mapping ID is required")
	}
	var probe UpstreamSourceBillingProbe
	if err := lockForUpdate(tx).Where("mapping_id = ?", mappingID).First(&probe).Error; err != nil {
		return nil, err
	}
	return &probe, nil
}

// LockUpstreamSourceBillingProbeIdentityTx freezes every database row used by
// the final identity comparison while a probe result is persisted.
func LockUpstreamSourceBillingProbeIdentityTx(tx *gorm.DB, sourceID int, mappingID int, leaseToken string) (*UpstreamSourceBillingProbeIdentityRows, error) {
	if tx == nil {
		return nil, errors.New("database transaction is required")
	}
	if sourceID == 0 || mappingID == 0 || strings.TrimSpace(leaseToken) == "" {
		return nil, errors.New("source, mapping, and lease token are required")
	}

	var rows UpstreamSourceBillingProbeIdentityRows
	probeResult := lockForUpdate(tx).
		Where("source_id = ? AND mapping_id = ? AND lease_token = ?", sourceID, mappingID, leaseToken).
		First(&rows.Probe)
	if probeResult.Error != nil {
		return nil, probeResult.Error
	}
	if err := lockForUpdate(tx).Where("id = ?", sourceID).First(&rows.Source).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).Where("id = ? AND source_id = ?", mappingID, sourceID).First(&rows.Mapping).Error; err != nil {
		return nil, err
	}
	if rows.Mapping.LocalChannelID == 0 {
		return &rows, nil
	}
	if err := lockForUpdate(tx).Where("id = ?", rows.Mapping.LocalChannelID).First(&rows.Channel).Error; err != nil {
		return nil, err
	}
	return &rows, nil
}

// LockUpstreamSourceBillingProbeCostRowsTx freezes the retained observation
// and every ownership row used by auto-priority before a complete cohort is
// persisted. The lock order matches probe result persistence.
func LockUpstreamSourceBillingProbeCostRowsTx(tx *gorm.DB, sourceID int, mappingID int, channelID int) (*UpstreamSourceBillingProbeIdentityRows, error) {
	if tx == nil {
		return nil, errors.New("database transaction is required")
	}
	if sourceID == 0 || mappingID == 0 || channelID == 0 {
		return nil, errors.New("source, mapping, and channel IDs are required")
	}

	var rows UpstreamSourceBillingProbeIdentityRows
	if err := lockForUpdate(tx).
		Where("source_id = ? AND mapping_id = ?", sourceID, mappingID).
		First(&rows.Probe).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).Where("id = ?", sourceID).First(&rows.Source).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).Where("id = ? AND source_id = ?", mappingID, sourceID).First(&rows.Mapping).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).Where("id = ?", channelID).First(&rows.Channel).Error; err != nil {
		return nil, err
	}
	return &rows, nil
}
