package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
)

const (
	AccountPoolSchedulabilityReasonReady                = "ready"
	AccountPoolSchedulabilityReasonNotBound             = "not_bound"
	AccountPoolSchedulabilityReasonNoSchedulableAccount = "no_schedulable_account"
)

type AccountPoolSchedulabilityRequest struct {
	ChannelID            int
	RequestModel         string
	ChannelUpstreamModel string
	Now                  int64
}

type AccountPoolSchedulabilityResult struct {
	RuntimeEnabled bool
	Schedulable    bool
	PoolID         int
	BindingID      int
	Reason         string
}

func CheckAccountPoolChannelSchedulability(req AccountPoolSchedulabilityRequest) (AccountPoolSchedulabilityResult, error) {
	now := req.Now
	if now == 0 {
		now = common.GetTimestamp()
	}
	binding, err := loadRuntimeAccountPoolBinding(AccountPoolSelectionRequest{
		ChannelID: req.ChannelID,
	})
	if err != nil {
		if errors.Is(err, ErrAccountPoolBindingNotRuntimeEnabled) {
			return AccountPoolSchedulabilityResult{
				Reason: AccountPoolSchedulabilityReasonNotBound,
			}, nil
		}
		return AccountPoolSchedulabilityResult{}, err
	}

	result := AccountPoolSchedulabilityResult{
		RuntimeEnabled: true,
		PoolID:         binding.PoolID,
		BindingID:      binding.Id,
	}
	_, err = SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            req.ChannelID,
		BindingID:            binding.Id,
		RequestModel:         req.RequestModel,
		ChannelUpstreamModel: req.ChannelUpstreamModel,
		Now:                  now,
	})
	if err != nil {
		if errors.Is(err, ErrAccountPoolNoSchedulableAccount) {
			result.Reason = AccountPoolSchedulabilityReasonNoSchedulableAccount
			return result, nil
		}
		return result, err
	}
	result.Schedulable = true
	result.Reason = AccountPoolSchedulabilityReasonReady
	return result, nil
}
