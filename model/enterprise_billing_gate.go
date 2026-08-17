package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// EnterpriseBillingRequired reports whether a caller is anchored to an
// enterprise while the jointly released enterprise billing path is enabled.
// A non-zero anchor is deliberately sufficient here: an inactive, draining,
// or otherwise invalid membership must be rejected by the enterprise reserve
// transaction, never silently fall back to personal funds.
func EnterpriseBillingRequired(userID int) (bool, error) {
	if !common.EnterpriseBillingEnabled {
		return false, nil
	}
	if DB == nil || userID <= 0 {
		return false, errors.New("invalid enterprise billing actor")
	}
	var user User
	if err := DB.Select("active_enterprise_id").Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return user.ActiveEnterpriseId > 0, nil
}
