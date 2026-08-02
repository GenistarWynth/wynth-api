package service

import (
	"context"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

const (
	upstreamSourceBillingProbeDefaultTimeout     = 10 * time.Second
	upstreamSourceBillingProbeDefaultBatchSize   = 20
	upstreamSourceBillingProbeDefaultConcurrency = 4
	upstreamSourceBillingProbeLeaseStaleAfter    = 30 * time.Second
	upstreamSourceBillingProbePersistenceTimeout = 2 * time.Second
)

var upstreamSourceBillingProbeSQLitePersistenceMu sync.Mutex

type UpstreamSourceBillingProbeService struct {
	now             func() time.Time
	requestTimeout  time.Duration
	jitter          func(time.Duration) time.Duration
	batchSize       int
	concurrency     int
	leaseStaleAfter time.Duration
}

func NewUpstreamSourceBillingProbeService() *UpstreamSourceBillingProbeService {
	return &UpstreamSourceBillingProbeService{
		now:             time.Now,
		requestTimeout:  upstreamSourceBillingProbeDefaultTimeout,
		jitter:          jitterUpstreamSourceBillingProbeInterval,
		batchSize:       upstreamSourceBillingProbeDefaultBatchSize,
		concurrency:     upstreamSourceBillingProbeDefaultConcurrency,
		leaseStaleAfter: upstreamSourceBillingProbeLeaseStaleAfter,
	}
}

func (service *UpstreamSourceBillingProbeService) currentTime() time.Time {
	if service != nil && service.now != nil {
		return service.now().UTC()
	}
	return time.Now().UTC()
}

func (service *UpstreamSourceBillingProbeService) timeout() time.Duration {
	if service == nil || service.requestTimeout <= 0 || service.requestTimeout > upstreamSourceBillingProbeDefaultTimeout {
		return upstreamSourceBillingProbeDefaultTimeout
	}
	return service.requestTimeout
}

func (service *UpstreamSourceBillingProbeService) staleLeaseSeconds() int64 {
	duration := upstreamSourceBillingProbeLeaseStaleAfter
	if service != nil && service.leaseStaleAfter > 0 {
		duration = service.leaseStaleAfter
	}
	seconds := int64(duration / time.Second)
	if seconds < 1 {
		return 1
	}
	return seconds
}

func lockUpstreamSourceBillingProbeDatabase() func() {
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		upstreamSourceBillingProbeSQLitePersistenceMu.Lock()
		return upstreamSourceBillingProbeSQLitePersistenceMu.Unlock
	}
	return func() {}
}

func persistUpstreamSourceBillingProbeTransaction(ctx context.Context, persist func(*gorm.DB) error) error {
	unlock := lockUpstreamSourceBillingProbeDatabase()
	defer unlock()
	return model.DB.WithContext(ctx).Transaction(persist)
}

func (service *UpstreamSourceBillingProbeService) nextDelay(intervalMinutes int, retryAfter time.Duration) time.Duration {
	interval := time.Duration(model.NormalizeUpstreamSourceBillingProbeInterval(intervalMinutes)) * time.Minute
	if service != nil && service.jitter != nil {
		interval = service.jitter(interval)
	}
	minimum := time.Duration(model.MinUpstreamSourceBillingProbeIntervalMinutes) * time.Minute * 4 / 5
	maximum := time.Duration(model.MaxUpstreamSourceBillingProbeIntervalMinutes) * time.Minute
	if interval < minimum {
		interval = minimum
	}
	if interval > maximum {
		interval = maximum
	}
	if retryAfter > interval {
		return retryAfter
	}
	return interval
}

func jitterUpstreamSourceBillingProbeInterval(interval time.Duration) time.Duration {
	jitterRange := interval / 5
	if jitterRange > 5*time.Minute {
		jitterRange = 5 * time.Minute
	}
	if jitterRange <= 0 {
		return interval
	}
	return interval + time.Duration(rand.Int64N(int64(jitterRange)*2+1)) - jitterRange
}

func (service *UpstreamSourceBillingProbeService) ProbeMapping(ctx context.Context, sourceID int, mappingID int) (*dto.UpstreamSourceBillingProbeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sourceID == 0 || mappingID == 0 {
		return nil, ErrUpstreamSourceBillingProbeNotEnabled
	}
	var configured model.UpstreamSourceBillingProbe
	unlock := lockUpstreamSourceBillingProbeDatabase()
	err := model.DB.WithContext(ctx).
		Where("source_id = ? AND mapping_id = ? AND enabled = ?", sourceID, mappingID, true).
		First(&configured).Error
	unlock()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrUpstreamSourceBillingProbeNotEnabled
	}
	if err != nil {
		return nil, err
	}
	return service.probeMapping(ctx, configured, false)
}

func (service *UpstreamSourceBillingProbeService) probeMapping(ctx context.Context, configured model.UpstreamSourceBillingProbe, requireDue bool) (response *dto.UpstreamSourceBillingProbeResponse, err error) {
	now := service.currentTime()
	token := common.GetUUID()
	unlock := lockUpstreamSourceBillingProbeDatabase()
	claimed, err := model.ClaimUpstreamSourceBillingProbeWithContext(
		ctx,
		configured.MappingID,
		token,
		now.Unix(),
		service.staleLeaseSeconds(),
		requireDue,
	)
	unlock()
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		if requireDue {
			return nil, nil
		}
		return nil, ErrUpstreamSourceBillingProbeLeaseUnavailable
	}
	if claimed.SourceID != configured.SourceID {
		releaseCtx, cancel := context.WithTimeout(context.Background(), upstreamSourceBillingProbePersistenceTimeout)
		defer cancel()
		unlock := lockUpstreamSourceBillingProbeDatabase()
		_, releaseErr := model.ReleaseUpstreamSourceBillingProbeLeaseWithContext(
			releaseCtx,
			claimed.MappingID,
			token,
			claimed.NextProbeAt,
			now.Unix(),
		)
		unlock()
		if releaseErr != nil {
			return nil, releaseErr
		}
		return nil, ErrUpstreamSourceBillingProbeIdentityChanged
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			service.recoverPanickedClaim(claimed, token)
			response = nil
			err = ErrUpstreamSourceBillingProbePanicked
		}
	}()

	identity, identityErr := loadUpstreamSourceBillingProbeIdentity(ctx, model.DB, claimed.SourceID, claimed.MappingID)
	if identityErr != nil {
		return service.completeUnidentifiedFailure(claimed, token, identityErr.code, 0, now)
	}
	identityStored, err := service.storeClaimIdentity(ctx, claimed, token, identity.Fingerprint, now.Unix())
	if err != nil {
		return nil, err
	}
	if !identityStored {
		_ = service.discardClaim(claimed.SourceID, claimed.MappingID, token, now)
		return nil, ErrUpstreamSourceBillingProbeIdentityChanged
	}

	if err := identity.FetchPolicy.validateURL(identity.RequestURL); err != nil {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorNetworkNotAllowed, 0, now, 0)
	}
	client, err := newUpstreamSourceBillingProbeHTTPClient(identity, service.timeout())
	if err != nil {
		code := UpstreamSourceBillingProbeErrorInvalidProxy
		if identityError, ok := err.(*upstreamSourceBillingProbeIdentityError); ok {
			code = identityError.code
		}
		return service.completeFailure(claimed, token, identity, code, 0, now, 0)
	}

	requestCtx, cancel := context.WithTimeout(ctx, service.timeout())
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, identity.RequestURL, nil)
	if err != nil {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorRequestBuild, 0, now, 0)
	}
	if err := applyUpstreamSourceBillingProbeHeaderOverrides(request.Header, identity.HeaderOverride, identity.BearerKey); err != nil {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorIneligible, 0, now, 0)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+identity.BearerKey)

	upstreamResponse, err := client.Do(request)
	if err != nil {
		code := UpstreamSourceBillingProbeErrorRequestFailed
		switch {
		case errors.Is(err, errUpstreamSourceBillingProbeRedirect):
			code = UpstreamSourceBillingProbeErrorRedirectNotAllowed
		case errors.Is(requestCtx.Err(), context.DeadlineExceeded):
			code = UpstreamSourceBillingProbeErrorRequestTimeout
		default:
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				code = UpstreamSourceBillingProbeErrorRequestTimeout
			}
		}
		return service.completeFailure(claimed, token, identity, code, 0, service.currentTime(), 0)
	}
	if upstreamResponse == nil || upstreamResponse.Body == nil {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorRequestFailed, 0, service.currentTime(), 0)
	}
	defer upstreamResponse.Body.Close()
	receivedAt := service.currentTime()
	if upstreamResponse.StatusCode == http.StatusTooManyRequests {
		return service.completeFailure(
			claimed,
			token,
			identity,
			UpstreamSourceBillingProbeErrorRateLimited,
			upstreamResponse.StatusCode,
			receivedAt,
			upstreamSourceBillingProbeRetryAfter(upstreamResponse.Header, receivedAt),
		)
	}
	if upstreamResponse.StatusCode == http.StatusNotFound || upstreamResponse.StatusCode == http.StatusMethodNotAllowed {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorUnsupported, upstreamResponse.StatusCode, receivedAt, 0)
	}
	body, readErr := io.ReadAll(io.LimitReader(upstreamResponse.Body, UpstreamSourceBillingProbeMaxBodyBytes+1))
	if readErr != nil {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorResponseRead, upstreamResponse.StatusCode, receivedAt, 0)
	}
	if len(body) > UpstreamSourceBillingProbeMaxBodyBytes {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorResponseTooLarge, upstreamResponse.StatusCode, receivedAt, 0)
	}

	switch upstreamResponse.StatusCode {
	case http.StatusUnauthorized:
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorUnauthorized, upstreamResponse.StatusCode, receivedAt, 0)
	case http.StatusForbidden:
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorForbidden, upstreamResponse.StatusCode, receivedAt, 0)
	}
	if upstreamResponse.StatusCode < http.StatusOK || upstreamResponse.StatusCode >= http.StatusMultipleChoices {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorHTTP, upstreamResponse.StatusCode, receivedAt, 0)
	}
	observation, err := parseUpstreamSourceBillingResponse(body, receivedAt)
	if err != nil {
		return service.completeFailure(claimed, token, identity, UpstreamSourceBillingProbeErrorInvalidResponse, upstreamResponse.StatusCode, receivedAt, 0)
	}
	return service.completeSuccess(claimed, token, identity, observation, upstreamResponse.StatusCode, receivedAt)
}

func (service *UpstreamSourceBillingProbeService) recoverPanickedClaim(claimed *model.UpstreamSourceBillingProbe, token string) {
	if claimed == nil || token == "" {
		return
	}
	finishedAt := service.currentTime()
	nextProbeAt := finishedAt.Add(
		time.Duration(model.NormalizeUpstreamSourceBillingProbeInterval(claimed.IntervalMinutes)) * time.Minute,
	).Unix()
	persistCtx, cancel := context.WithTimeout(context.Background(), upstreamSourceBillingProbePersistenceTimeout)
	defer cancel()
	unlock := lockUpstreamSourceBillingProbeDatabase()
	defer unlock()
	_, _ = model.ReleaseUpstreamSourceBillingProbeLeaseWithContext(
		persistCtx,
		claimed.MappingID,
		token,
		nextProbeAt,
		finishedAt.Unix(),
	)
}

func (service *UpstreamSourceBillingProbeService) storeClaimIdentity(ctx context.Context, claimed *model.UpstreamSourceBillingProbe, token string, fingerprint string, now int64) (bool, error) {
	stored := false
	err := persistUpstreamSourceBillingProbeTransaction(ctx, func(tx *gorm.DB) error {
		rows, err := model.LockUpstreamSourceBillingProbeIdentityTx(tx, claimed.SourceID, claimed.MappingID, token)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !rows.Probe.Enabled || rows.Probe.IntervalMinutes != claimed.IntervalMinutes {
			return nil
		}
		_, matches := matchUpstreamSourceBillingProbeIdentityRows(rows, fingerprint)
		if !matches {
			return nil
		}
		if err := tx.Model(&model.UpstreamSourceBillingProbe{}).
			Where("source_id = ? AND mapping_id = ? AND lease_token = ?", claimed.SourceID, claimed.MappingID, token).
			Updates(map[string]any{
				"claim_identity_fingerprint": fingerprint,
				"updated_time":               now,
			}).Error; err != nil {
			return err
		}
		stored = true
		return nil
	})
	return stored, err
}

func updateClaimedUpstreamSourceBillingProbeTx(
	tx *gorm.DB,
	sourceID int,
	mappingID int,
	token string,
	updates map[string]any,
) error {
	result := tx.Model(&model.UpstreamSourceBillingProbe{}).
		Where("source_id = ? AND mapping_id = ? AND lease_token = ?", sourceID, mappingID, token).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("billing probe claim update affected an unexpected number of rows")
	}
	return nil
}

func (service *UpstreamSourceBillingProbeService) completeSuccess(
	claimed *model.UpstreamSourceBillingProbe,
	token string,
	identity upstreamSourceBillingProbeRequestIdentity,
	observation upstreamSourceBillingObservation,
	httpStatus int,
	receivedAt time.Time,
) (*dto.UpstreamSourceBillingProbeResponse, error) {
	identityChanged := false
	persistCtx, cancel := context.WithTimeout(context.Background(), upstreamSourceBillingProbePersistenceTimeout)
	defer cancel()
	err := persistUpstreamSourceBillingProbeTransaction(persistCtx, func(tx *gorm.DB) error {
		rows, currentIdentity, changed, err := lockAndCompareUpstreamSourceBillingProbeIdentity(tx, claimed, token, identity, receivedAt)
		if err != nil {
			return err
		}
		if changed {
			identityChanged = true
			return nil
		}
		freshUntil := receivedAt.Add(2 * time.Duration(rows.Probe.IntervalMinutes) * time.Minute)
		nextProbeAt := receivedAt.Add(service.nextDelay(rows.Probe.IntervalMinutes, 0))
		groupRate := observation.GroupRateMultiplier
		resolvedRate := observation.ResolvedRateMultiplier
		peakEnabled := observation.PeakRateEnabled
		effectiveRate := observation.EffectiveRateMultiplier
		return updateClaimedUpstreamSourceBillingProbeTx(tx, claimed.SourceID, claimed.MappingID, token, map[string]any{
			"status":                         model.UpstreamSourceBillingProbeStatusOK,
			"error_code":                     "",
			"http_status":                    httpStatus,
			"failure_count":                  0,
			"group_rate_multiplier":          &groupRate,
			"user_rate_multiplier":           observation.UserRateMultiplier,
			"resolved_rate_multiplier":       &resolvedRate,
			"peak_rate_enabled":              &peakEnabled,
			"peak_start":                     observation.PeakStart,
			"peak_end":                       observation.PeakEnd,
			"peak_rate_multiplier":           observation.PeakRateMultiplier,
			"applied_peak_multiplier":        observation.AppliedPeakMultiplier,
			"effective_rate_multiplier":      &effectiveRate,
			"timezone":                       observation.Timezone,
			"observed_at":                    observation.ObservedAt.UTC().Format(time.RFC3339Nano),
			"last_attempt_at":                receivedAt.Unix(),
			"received_at":                    receivedAt.Unix(),
			"fresh_until":                    freshUntil.Unix(),
			"next_probe_at":                  nextProbeAt.Unix(),
			"last_good_generation":           rows.Probe.LastGoodGeneration + 1,
			"last_good_identity_fingerprint": currentIdentity.Fingerprint,
			"claim_identity_fingerprint":     "",
			"lease_token":                    "",
			"lease_started_at":               0,
			"updated_time":                   receivedAt.Unix(),
		})
	})
	if err != nil {
		return nil, err
	}
	if identityChanged {
		return nil, ErrUpstreamSourceBillingProbeIdentityChanged
	}
	return service.GetMapping(context.Background(), claimed.SourceID, claimed.MappingID, receivedAt)
}

func (service *UpstreamSourceBillingProbeService) completeFailure(
	claimed *model.UpstreamSourceBillingProbe,
	token string,
	identity upstreamSourceBillingProbeRequestIdentity,
	code string,
	httpStatus int,
	finishedAt time.Time,
	retryAfter time.Duration,
) (*dto.UpstreamSourceBillingProbeResponse, error) {
	identityChanged := false
	persistCtx, cancel := context.WithTimeout(context.Background(), upstreamSourceBillingProbePersistenceTimeout)
	defer cancel()
	err := persistUpstreamSourceBillingProbeTransaction(persistCtx, func(tx *gorm.DB) error {
		rows, _, changed, err := lockAndCompareUpstreamSourceBillingProbeIdentity(tx, claimed, token, identity, finishedAt)
		if err != nil {
			return err
		}
		if changed {
			identityChanged = true
			return nil
		}
		status := model.UpstreamSourceBillingProbeStatusFailed
		if code == UpstreamSourceBillingProbeErrorUnsupported ||
			rows.Probe.Status == model.UpstreamSourceBillingProbeStatusUnsupported {
			status = model.UpstreamSourceBillingProbeStatusUnsupported
		}
		nextProbeTime := finishedAt.Add(service.nextDelay(rows.Probe.IntervalMinutes, retryAfter))
		nextProbeAt := nextProbeTime.Unix()
		if retryAfter > 0 && nextProbeTime.Nanosecond() > 0 && nextProbeAt < math.MaxInt64 {
			nextProbeAt++
		}
		return updateClaimedUpstreamSourceBillingProbeTx(tx, claimed.SourceID, claimed.MappingID, token, map[string]any{
			"status":                     status,
			"error_code":                 sanitizeUpstreamSourceBillingProbeErrorCode(code),
			"http_status":                httpStatus,
			"failure_count":              rows.Probe.FailureCount + 1,
			"last_attempt_at":            finishedAt.Unix(),
			"next_probe_at":              nextProbeAt,
			"claim_identity_fingerprint": "",
			"lease_token":                "",
			"lease_started_at":           0,
			"updated_time":               finishedAt.Unix(),
		})
	})
	if err != nil {
		return nil, err
	}
	if identityChanged {
		return nil, ErrUpstreamSourceBillingProbeIdentityChanged
	}
	return service.GetMapping(context.Background(), claimed.SourceID, claimed.MappingID, finishedAt)
}

func (service *UpstreamSourceBillingProbeService) completeUnidentifiedFailure(
	claimed *model.UpstreamSourceBillingProbe,
	token string,
	code string,
	httpStatus int,
	finishedAt time.Time,
) (*dto.UpstreamSourceBillingProbeResponse, error) {
	identityChanged := false
	persistCtx, cancel := context.WithTimeout(context.Background(), upstreamSourceBillingProbePersistenceTimeout)
	defer cancel()
	err := persistUpstreamSourceBillingProbeTransaction(persistCtx, func(tx *gorm.DB) error {
		rows, err := model.LockUpstreamSourceBillingProbeIdentityTx(tx, claimed.SourceID, claimed.MappingID, token)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				identityChanged = true
				_, discardErr := discardUpstreamSourceBillingProbeClaimTx(
					tx,
					claimed.SourceID,
					claimed.MappingID,
					token,
					claimed.Enabled,
					finishedAt.Unix(),
				)
				return discardErr
			}
			return err
		}
		probe := &rows.Probe
		if probe.LeaseToken != token ||
			probe.SourceID != claimed.SourceID ||
			probe.MappingID != claimed.MappingID ||
			probe.Enabled != claimed.Enabled ||
			probe.IntervalMinutes != claimed.IntervalMinutes ||
			probe.ClaimIdentityFingerprint != claimed.ClaimIdentityFingerprint ||
			!model.IsUpstreamSourceBillingProbeIdentityRowsEligible(rows) {
			identityChanged = true
			_, err := discardUpstreamSourceBillingProbeClaimTx(tx, claimed.SourceID, claimed.MappingID, token, probe.Enabled, finishedAt.Unix())
			return err
		}
		status := model.UpstreamSourceBillingProbeStatusFailed
		if probe.Status == model.UpstreamSourceBillingProbeStatusUnsupported {
			status = model.UpstreamSourceBillingProbeStatusUnsupported
		}
		nextProbeAt := finishedAt.Add(service.nextDelay(probe.IntervalMinutes, 0))
		return updateClaimedUpstreamSourceBillingProbeTx(tx, claimed.SourceID, claimed.MappingID, token, map[string]any{
			"status":                     status,
			"error_code":                 sanitizeUpstreamSourceBillingProbeErrorCode(code),
			"http_status":                httpStatus,
			"failure_count":              probe.FailureCount + 1,
			"last_attempt_at":            finishedAt.Unix(),
			"next_probe_at":              nextProbeAt.Unix(),
			"claim_identity_fingerprint": "",
			"lease_token":                "",
			"lease_started_at":           0,
			"updated_time":               finishedAt.Unix(),
		})
	})
	if err != nil {
		return nil, err
	}
	if identityChanged {
		return nil, ErrUpstreamSourceBillingProbeIdentityChanged
	}
	return service.GetMapping(context.Background(), claimed.SourceID, claimed.MappingID, finishedAt)
}

func lockAndCompareUpstreamSourceBillingProbeIdentity(
	tx *gorm.DB,
	claimed *model.UpstreamSourceBillingProbe,
	token string,
	expected upstreamSourceBillingProbeRequestIdentity,
	now time.Time,
) (*model.UpstreamSourceBillingProbeIdentityRows, upstreamSourceBillingProbeRequestIdentity, bool, error) {
	rows, err := model.LockUpstreamSourceBillingProbeIdentityTx(tx, claimed.SourceID, claimed.MappingID, token)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if _, releaseErr := discardUpstreamSourceBillingProbeClaimTx(tx, claimed.SourceID, claimed.MappingID, token, true, now.Unix()); releaseErr != nil {
				return nil, upstreamSourceBillingProbeRequestIdentity{}, false, releaseErr
			}
			return nil, upstreamSourceBillingProbeRequestIdentity{}, true, nil
		}
		return nil, upstreamSourceBillingProbeRequestIdentity{}, false, err
	}
	current, matches := matchUpstreamSourceBillingProbeIdentityRows(rows, expected.Fingerprint)
	if !matches || rows.Probe.ClaimIdentityFingerprint != expected.Fingerprint {
		if _, err := discardUpstreamSourceBillingProbeClaimTx(tx, claimed.SourceID, claimed.MappingID, token, rows.Probe.Enabled, now.Unix()); err != nil {
			return nil, upstreamSourceBillingProbeRequestIdentity{}, false, err
		}
		return rows, upstreamSourceBillingProbeRequestIdentity{}, true, nil
	}
	return rows, current, false, nil
}

func discardUpstreamSourceBillingProbeClaimTx(tx *gorm.DB, sourceID int, mappingID int, token string, enabled bool, now int64) (bool, error) {
	nextProbeAt := int64(0)
	if enabled {
		nextProbeAt = now
	}
	result := tx.Model(&model.UpstreamSourceBillingProbe{}).
		Where("source_id = ? AND mapping_id = ? AND lease_token = ?", sourceID, mappingID, token).
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
		return false, errors.New("billing probe claim discard affected multiple rows")
	}
	return result.RowsAffected == 1, nil
}

func (service *UpstreamSourceBillingProbeService) discardClaim(sourceID int, mappingID int, token string, now time.Time) error {
	persistCtx, cancel := context.WithTimeout(context.Background(), upstreamSourceBillingProbePersistenceTimeout)
	defer cancel()
	return persistUpstreamSourceBillingProbeTransaction(persistCtx, func(tx *gorm.DB) error {
		probe, err := model.LockUpstreamSourceBillingProbeTx(tx, mappingID)
		if err != nil {
			return err
		}
		if probe.SourceID != sourceID {
			return gorm.ErrRecordNotFound
		}
		discarded, err := discardUpstreamSourceBillingProbeClaimTx(tx, sourceID, mappingID, token, probe.Enabled, now.Unix())
		if err != nil {
			return err
		}
		if !discarded {
			return errors.New("billing probe claim discard affected no rows")
		}
		return nil
	})
}

func sanitizeUpstreamSourceBillingProbeErrorCode(code string) string {
	switch code {
	case UpstreamSourceBillingProbeErrorIneligible,
		UpstreamSourceBillingProbeErrorInvalidBaseURL,
		UpstreamSourceBillingProbeErrorInvalidProxy,
		UpstreamSourceBillingProbeErrorNetworkNotAllowed,
		UpstreamSourceBillingProbeErrorRequestBuild,
		UpstreamSourceBillingProbeErrorRequestFailed,
		UpstreamSourceBillingProbeErrorRequestTimeout,
		UpstreamSourceBillingProbeErrorRedirectNotAllowed,
		UpstreamSourceBillingProbeErrorResponseRead,
		UpstreamSourceBillingProbeErrorResponseTooLarge,
		UpstreamSourceBillingProbeErrorUnauthorized,
		UpstreamSourceBillingProbeErrorForbidden,
		UpstreamSourceBillingProbeErrorUnsupported,
		UpstreamSourceBillingProbeErrorRateLimited,
		UpstreamSourceBillingProbeErrorHTTP,
		UpstreamSourceBillingProbeErrorInvalidResponse:
		return code
	default:
		return UpstreamSourceBillingProbeErrorRequestFailed
	}
}

func (service *UpstreamSourceBillingProbeService) GetMapping(ctx context.Context, sourceID int, mappingID int, now time.Time) (*dto.UpstreamSourceBillingProbeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	unlock := lockUpstreamSourceBillingProbeDatabase()
	var probe model.UpstreamSourceBillingProbe
	probeErr := model.DB.WithContext(ctx).Where("source_id = ? AND mapping_id = ?", sourceID, mappingID).First(&probe).Error
	if probeErr != nil && !errors.Is(probeErr, gorm.ErrRecordNotFound) {
		unlock()
		return nil, probeErr
	}
	var mapping model.UpstreamSourceChannelMapping
	mappingErr := model.DB.WithContext(ctx).Where("id = ? AND source_id = ?", mappingID, sourceID).First(&mapping).Error
	var source model.UpstreamSource
	sourceErr := model.DB.WithContext(ctx).Where("id = ?", sourceID).First(&source).Error
	var channel model.Channel
	channelErr := gorm.ErrRecordNotFound
	if mappingErr == nil && mapping.LocalChannelID > 0 {
		channelErr = model.DB.WithContext(ctx).Where("id = ?", mapping.LocalChannelID).First(&channel).Error
	}
	unlock()
	if mappingErr != nil {
		return nil, mappingErr
	}
	if sourceErr != nil && !errors.Is(sourceErr, gorm.ErrRecordNotFound) {
		return nil, sourceErr
	}
	if channelErr != nil && !errors.Is(channelErr, gorm.ErrRecordNotFound) {
		return nil, channelErr
	}
	response := &dto.UpstreamSourceBillingProbeResponse{
		SourceID:               sourceID,
		MappingID:              mappingID,
		Enabled:                false,
		IntervalMinutes:        model.DefaultUpstreamSourceBillingProbeIntervalMinutes,
		Status:                 model.UpstreamSourceBillingProbeStatusIdle,
		AutoPriorityCostSource: normalizedUpstreamSourceAutoPriorityCostSource(mapping.AutoPriorityCostSource),
		EmpiricalStatus:        "missing",
	}
	response.AdvertisedEffectiveRateMultiplier = mapping.EffectiveRateMultiplier
	if errors.Is(probeErr, gorm.ErrRecordNotFound) {
		return response, nil
	}
	response.Enabled = probe.Enabled
	response.IntervalMinutes = probe.IntervalMinutes
	response.Status = probe.Status
	response.ErrorCode = sanitizeUpstreamSourceBillingProbeErrorCode(probe.ErrorCode)
	if probe.ErrorCode == "" {
		response.ErrorCode = ""
	}
	response.Unsupported = probe.Status == model.UpstreamSourceBillingProbeStatusUnsupported
	response.GroupRateMultiplier = probe.GroupRateMultiplier
	response.UserRateMultiplier = probe.UserRateMultiplier
	response.ResolvedRateMultiplier = probe.ResolvedRateMultiplier
	response.PeakRateEnabled = probe.PeakRateEnabled
	response.PeakStart = probe.PeakStart
	response.PeakEnd = probe.PeakEnd
	response.PeakRateMultiplier = probe.PeakRateMultiplier
	response.AppliedPeakMultiplier = probe.AppliedPeakMultiplier
	response.EffectiveRateMultiplier = probe.EffectiveRateMultiplier
	response.Timezone = probe.Timezone
	response.ObservedAt = probe.ObservedAt
	response.LastAttemptAt = probe.LastAttemptAt
	response.ReceivedAt = probe.ReceivedAt
	response.FreshUntil = probe.FreshUntil
	response.HasLastGood = probe.ReceivedAt > 0 && probe.GroupRateMultiplier != nil &&
		probe.ResolvedRateMultiplier != nil && probe.EffectiveRateMultiplier != nil
	empiricalMapping := mapping
	empiricalMapping.AutoPriorityCostSource = model.UpstreamSourceAutoPriorityCostSourceEmpiricalProbe
	var sourcePointer *model.UpstreamSource
	if sourceErr == nil {
		sourcePointer = &source
	}
	var channelPointer *model.Channel
	if channelErr == nil {
		channelPointer = &channel
	}
	resolved, reason := resolveUpstreamSourceAutoPriorityCost(now, sourcePointer, &empiricalMapping, channelPointer, &probe)
	switch reason {
	case "":
		if probe.Status == model.UpstreamSourceBillingProbeStatusFailed {
			response.EmpiricalStatus = "temporary_failed"
		} else {
			response.EmpiricalStatus = "ready"
		}
		response.EmpiricalNominalRateMultiplier = common.GetPointer(resolved.nominalRateMultiplier)
	case "empirical_probe_disabled":
		response.EmpiricalStatus = "disabled"
	case "empirical_probe_missing":
		response.EmpiricalStatus = "missing"
	case "empirical_probe_stale":
		response.EmpiricalStatus = "stale"
	case "empirical_probe_unsupported":
		response.EmpiricalStatus = "unsupported"
	case "empirical_probe_identity_mismatch", "empirical_probe_ineligible":
		response.EmpiricalStatus = "identity_mismatch"
	default:
		response.EmpiricalStatus = "malformed"
	}
	response.Fresh = reason == ""
	response.Stale = response.HasLastGood && !response.Fresh
	return response, nil
}

func UpdateUpstreamSourceBillingProbe(
	ctx context.Context,
	sourceID int,
	mappingID int,
	enabled bool,
	intervalMinutes int,
	autoPriorityCostSource string,
	now time.Time,
) (*dto.UpstreamSourceBillingProbeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sourceID == 0 || mappingID == 0 {
		return nil, errors.New("source and mapping IDs are required")
	}
	if intervalMinutes < model.MinUpstreamSourceBillingProbeIntervalMinutes || intervalMinutes > model.MaxUpstreamSourceBillingProbeIntervalMinutes {
		return nil, errors.New("billing probe interval is out of range")
	}
	autoPriorityCostSource, validCostSource := model.ResolveUpstreamSourceAutoPriorityCostSource(autoPriorityCostSource)
	if !validCostSource {
		return nil, errors.New("auto-priority cost source is invalid")
	}
	now = now.UTC()
	err := persistUpstreamSourceBillingProbeTransaction(ctx, func(tx *gorm.DB) error {
		var source model.UpstreamSource
		if err := tx.Where("id = ? AND type = ? AND status <> ?", sourceID, model.UpstreamSourceTypeSub2API, model.UpstreamSourceStatusDeleted).
			First(&source).Error; err != nil {
			return err
		}
		var mapping model.UpstreamSourceChannelMapping
		if err := tx.Where("id = ? AND source_id = ?", mappingID, sourceID).First(&mapping).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.UpstreamSourceChannelMapping{}).
			Where("id = ? AND source_id = ?", mappingID, sourceID).
			Update("auto_priority_cost_source", autoPriorityCostSource).Error; err != nil {
			return err
		}
		probe, err := model.LockUpstreamSourceBillingProbeTx(tx, mappingID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			nextProbeAt := int64(0)
			if enabled {
				nextProbeAt = now.Unix()
			}
			result := tx.Create(&model.UpstreamSourceBillingProbe{
				SourceID:        sourceID,
				MappingID:       mappingID,
				Enabled:         enabled,
				IntervalMinutes: intervalMinutes,
				Status:          model.UpstreamSourceBillingProbeStatusIdle,
				NextProbeAt:     nextProbeAt,
				CreatedTime:     now.Unix(),
				UpdatedTime:     now.Unix(),
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return errors.New("billing probe configuration create affected an unexpected number of rows")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if probe.SourceID != sourceID {
			return gorm.ErrRecordNotFound
		}
		nextProbeAt := int64(0)
		if enabled {
			nextProbeAt = probe.NextProbeAt
			if !probe.Enabled || probe.IntervalMinutes != intervalMinutes || nextProbeAt == 0 {
				nextProbeAt = now.Unix()
			}
		}
		result := tx.Model(&model.UpstreamSourceBillingProbe{}).
			Where("source_id = ? AND mapping_id = ?", sourceID, mappingID).
			Updates(map[string]any{
				"enabled":                    enabled,
				"interval_minutes":           intervalMinutes,
				"next_probe_at":              nextProbeAt,
				"claim_identity_fingerprint": "",
				"updated_time":               now.Unix(),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("billing probe configuration update affected an unexpected number of rows")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewUpstreamSourceBillingProbeService().GetMapping(ctx, sourceID, mappingID, now)
}

func normalizedUpstreamSourceAutoPriorityCostSource(value string) string {
	resolved, valid := model.ResolveUpstreamSourceAutoPriorityCostSource(value)
	if !valid {
		return model.UpstreamSourceAutoPriorityCostSourceAdvertised
	}
	return resolved
}

func (service *UpstreamSourceBillingProbeService) RunDue(ctx context.Context) ([]dto.UpstreamSourceBillingProbeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	batchSize := upstreamSourceBillingProbeDefaultBatchSize
	if service != nil && service.batchSize > 0 && service.batchSize <= 1000 {
		batchSize = service.batchSize
	}
	unlock := lockUpstreamSourceBillingProbeDatabase()
	probes, err := model.ListDueUpstreamSourceBillingProbesWithContext(ctx, service.currentTime().Unix(), batchSize)
	unlock()
	if err != nil || len(probes) == 0 {
		return nil, err
	}
	concurrency := upstreamSourceBillingProbeDefaultConcurrency
	if service != nil && service.concurrency > 0 && service.concurrency <= batchSize {
		concurrency = service.concurrency
	}
	if concurrency > len(probes) {
		concurrency = len(probes)
	}

	type runResult struct {
		index    int
		response *dto.UpstreamSourceBillingProbeResponse
		err      error
	}
	jobs := make(chan int)
	results := make(chan runResult, len(probes))
	var workers sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				func(index int) {
					result := runResult{index: index}
					defer func() {
						if recovered := recover(); recovered != nil {
							result.response = nil
							result.err = ErrUpstreamSourceBillingProbePanicked
						}
						results <- result
					}()
					if ctx.Err() != nil {
						result.err = ctx.Err()
						return
					}
					result.response, result.err = service.probeMapping(ctx, probes[index], true)
				}(index)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range probes {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	ordered := make([]*dto.UpstreamSourceBillingProbeResponse, len(probes))
	var firstErr error
	for result := range results {
		ordered[result.index] = result.response
		if result.err != nil && !errors.Is(result.err, ErrUpstreamSourceBillingProbeLeaseUnavailable) && firstErr == nil {
			firstErr = result.err
		}
	}
	responses := make([]dto.UpstreamSourceBillingProbeResponse, 0, len(ordered))
	for _, response := range ordered {
		if response != nil {
			responses = append(responses, *response)
		}
	}
	return responses, firstErr
}
