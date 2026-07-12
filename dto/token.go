package dto

type TokenResponse struct {
	Id                 int     `json:"id"`
	UserId             int     `json:"user_id"`
	Key                string  `json:"key"`
	Status             int     `json:"status"`
	Name               string  `json:"name"`
	CreatedTime        int64   `json:"created_time"`
	AccessedTime       int64   `json:"accessed_time"`
	ExpiredTime        int64   `json:"expired_time"`
	RemainQuota        int     `json:"remain_quota"`
	UnlimitedQuota     bool    `json:"unlimited_quota"`
	ModelLimitsEnabled bool    `json:"model_limits_enabled"`
	ModelLimits        string  `json:"model_limits"`
	AllowIps           *string `json:"allow_ips"`
	UsedQuota          int     `json:"used_quota"`
	Group              string  `json:"group"`
	CrossGroupRetry    bool    `json:"cross_group_retry"`
	Concurrency        int     `json:"concurrency"`
}
