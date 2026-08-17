package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
)

// PersonalAssetsFrozen uses only the server-side membership state. Removed is
// intentionally absent: any other membership state keeps personal assets
// frozen until the removal workflow has completed.
func PersonalAssetsFrozen(userID int) (bool, error) {
	if userID <= 0 {
		return false, errors.New("invalid user id")
	}
	if !common.EnterpriseBillingEnabled {
		return false, nil
	}
	var count int64
	err := DB.Model(&EnterpriseMembership{}).
		Where("user_id = ? AND status IN ?", userID, []int{
			EnterpriseMembershipStatusActive,
			EnterpriseMembershipStatusPaused,
			EnterpriseMembershipStatusDraining,
			EnterpriseMembershipStatusReclaimed,
			EnterpriseMembershipStatusManualReview,
		}).
		Count(&count).Error
	return count > 0, err
}
