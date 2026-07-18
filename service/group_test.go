package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetUserUsableGroupsDoesNotIncludeSampleSpecialGroup(t *testing.T) {
	groups := GetUserUsableGroups("vip")

	assert.Contains(t, groups, "vip")
	assert.NotContains(t, groups, "append_1")
}
