package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	EnterpriseStatusActive  = 1
	EnterpriseStatusClosing = 2
	EnterpriseStatusClosed  = 3
)

const (
	EnterpriseMembershipRoleOwner  = 1
	EnterpriseMembershipRoleMember = 2
)

const (
	EnterpriseMembershipStatusActive    = 1
	EnterpriseMembershipStatusPaused    = 2
	EnterpriseMembershipStatusDraining  = 3
	EnterpriseMembershipStatusReclaimed = 4
	EnterpriseMembershipStatusRemoved   = 5
	// Manual review remains an active relationship for anchor purposes. Keep
	// the existing values stable so a migration never reinterprets history.
	EnterpriseMembershipStatusManualReview = 6
)

const (
	EnterpriseInvitationStatusPending    = 1
	EnterpriseInvitationStatusAccepted   = 2
	EnterpriseInvitationStatusRevoked    = 3
	EnterpriseInvitationStatusExpired    = 4
	EnterpriseInvitationStatusConflicted = 5
	EnterpriseInvitationStatusRejected   = 6
)

const (
	EnterpriseLedgerKindTopUp      = "TOPUP"
	EnterpriseLedgerKindAllocate   = "ALLOCATE"
	EnterpriseLedgerKindReclaim    = "RECLAIM"
	EnterpriseLedgerKindReserve    = "RESERVE"
	EnterpriseLedgerKindSettle     = "SETTLE"
	EnterpriseLedgerKindRefund     = "REFUND"
	EnterpriseLedgerKindAnomaly    = "ANOMALY"
	EnterpriseLedgerKindAdjustment = "ADJUSTMENT"
	EnterpriseLedgerKindReversal   = "REVERSAL"
)

const (
	APIKeyDeliveryStatusPending   = "PENDING_DELIVERY"
	APIKeyDeliveryStatusDelivered = "DELIVERED"
	APIKeyDeliveryStatusFailed    = "FAILED_DELIVERY"
)

// Enterprise is the independent billing subject for an enterprise owner.
type Enterprise struct {
	Id             int    `json:"id"`
	Name           string `json:"name" gorm:"type:varchar(100);not null"`
	OwnerUserId    int    `json:"owner_user_id" gorm:"type:int;not null;uniqueIndex:idx_enterprise_owner_user"`
	Status         int    `json:"status" gorm:"type:int;not null;default:1;index"`
	AvailableQuota int    `json:"available_quota" gorm:"type:int;not null;default:0"`
	ReservedQuota  int    `json:"reserved_quota" gorm:"type:int;not null;default:0"`
	AnomalyQuota   int    `json:"anomaly_quota" gorm:"type:int;not null;default:0"`
	CreatedAt      int64  `json:"created_at" gorm:"type:bigint;not null"`
	UpdatedAt      int64  `json:"updated_at" gorm:"type:bigint;not null"`
	ClosedAt       int64  `json:"closed_at" gorm:"type:bigint;not null;default:0"`
}

// EnterpriseMembership records one user's enterprise relationship and its
// independent allocation counters. Historical rows remain after removal.
type EnterpriseMembership struct {
	Id                   int    `json:"id"`
	EnterpriseId         int    `json:"enterprise_id" gorm:"type:int;not null;index:idx_enterprise_membership_enterprise;uniqueIndex:idx_enterprise_membership_enterprise_user,priority:1"`
	UserId               int    `json:"user_id" gorm:"type:int;not null;index:idx_enterprise_membership_user;uniqueIndex:idx_enterprise_membership_enterprise_user,priority:2"`
	Role                 int    `json:"role" gorm:"type:int;not null"`
	Status               int    `json:"status" gorm:"type:int;not null;default:1;index"`
	AvailableQuota       int    `json:"available_quota" gorm:"type:int;not null;default:0"`
	ReservedQuota        int    `json:"reserved_quota" gorm:"type:int;not null;default:0"`
	SelfKeyLimit         int    `json:"self_key_limit" gorm:"type:int;not null;default:0"`
	SelfKeyUsedQuota     int    `json:"self_key_used_quota" gorm:"type:int;not null;default:0"`
	SelfKeyReservedQuota int    `json:"self_key_reserved_quota" gorm:"type:int;not null;default:0"`
	AllowIps             string `json:"allow_ips" gorm:"type:text"`
	JoinedAt             int64  `json:"joined_at" gorm:"type:bigint;not null"`
	PausedAt             int64  `json:"paused_at" gorm:"type:bigint;not null;default:0"`
	DrainingAt           int64  `json:"draining_at" gorm:"type:bigint;not null;default:0"`
	ReclaimedAt          int64  `json:"reclaimed_at" gorm:"type:bigint;not null;default:0"`
	RemovedAt            int64  `json:"removed_at" gorm:"type:bigint;not null;default:0"`
}

type EnterpriseInvitation struct {
	Id             int    `json:"id"`
	EnterpriseId   int    `json:"enterprise_id" gorm:"type:int;not null;index"`
	InviterUserId  int    `json:"inviter_user_id" gorm:"type:int;not null;index"`
	TargetEmail    string `json:"target_email" gorm:"type:varchar(255);not null;index"`
	Status         int    `json:"status" gorm:"type:int;not null;default:1;index"`
	ExpectedRole   int    `json:"expected_role" gorm:"type:int;not null;default:2;index"`
	AuthFlowId     int    `json:"auth_flow_id" gorm:"type:int;not null;default:0;index"`
	AcceptedUserId int    `json:"accepted_user_id" gorm:"type:int;not null;default:0;index"`
	ExpiresAt      int64  `json:"expires_at" gorm:"type:bigint;not null"`
	CreatedAt      int64  `json:"created_at" gorm:"type:bigint;not null"`
	AcceptedAt     int64  `json:"accepted_at" gorm:"type:bigint;not null;default:0"`
	RevokedAt      int64  `json:"revoked_at" gorm:"type:bigint;not null;default:0"`
	RejectedAt     int64  `json:"rejected_at" gorm:"type:bigint;not null;default:0"`
}

// EnterpriseLedger is append-only. Balance updates and a ledger row must be
// committed in the same transaction by later enterprise billing slices.
type EnterpriseLedger struct {
	Id                       int64  `json:"id"`
	EnterpriseId             int    `json:"enterprise_id" gorm:"type:int;not null;index"`
	MembershipId             *int   `json:"membership_id" gorm:"type:int;index"`
	Kind                     string `json:"kind" gorm:"type:varchar(40);not null;index"`
	EnterpriseAvailableDelta int    `json:"enterprise_available_delta" gorm:"type:int;not null;default:0"`
	EnterpriseReservedDelta  int    `json:"enterprise_reserved_delta" gorm:"type:int;not null;default:0"`
	MemberAvailableDelta     int    `json:"member_available_delta" gorm:"type:int;not null;default:0"`
	MemberReservedDelta      int    `json:"member_reserved_delta" gorm:"type:int;not null;default:0"`
	Amount                   int    `json:"amount" gorm:"type:int;not null"`
	ActorUserId              int    `json:"actor_user_id" gorm:"type:int;not null;default:0;index"`
	ReferenceType            string `json:"reference_type" gorm:"type:varchar(40);not null;default:''"`
	ReferenceId              string `json:"reference_id" gorm:"type:varchar(128);not null;default:'';index"`
	IdempotencyKey           string `json:"idempotency_key" gorm:"type:varchar(191);not null;uniqueIndex"`
	RequestId                string `json:"request_id" gorm:"type:varchar(64);not null;default:'';index"`
	Reason                   string `json:"reason" gorm:"type:varchar(255);not null;default:''"`
	CreatedAt                int64  `json:"created_at" gorm:"type:bigint;not null"`
}

type EnterpriseUsageRecord struct {
	Id                     int64  `json:"id"`
	EnterpriseId           int    `json:"enterprise_id" gorm:"type:int;not null;index"`
	MembershipId           int    `json:"membership_id" gorm:"type:int;not null;default:0;index"`
	ActorUserId            int    `json:"actor_user_id" gorm:"type:int;not null;index"`
	TokenId                int    `json:"token_id" gorm:"type:int;not null;default:0;index;uniqueIndex:idx_enterprise_usage_token_idempotency,priority:1"`
	IdempotencyKey         string `json:"idempotency_key" gorm:"type:varchar(191);not null;uniqueIndex:idx_enterprise_usage_token_idempotency,priority:2"`
	RequestFingerprint     string `json:"request_fingerprint" gorm:"type:varchar(191);not null;default:''"`
	RequestId              string `json:"request_id" gorm:"type:varchar(64);not null;index"`
	TaskId                 string `json:"task_id" gorm:"type:varchar(191);not null;default:'';index"`
	BillingAccountId       int    `json:"billing_account_id" gorm:"type:int;not null;default:0;index"`
	AllocationId           int    `json:"allocation_id" gorm:"type:int;not null;default:0;index"`
	FundingSource          string `json:"funding_source" gorm:"type:varchar(32);not null"`
	MembershipRoleSnapshot int    `json:"membership_role_snapshot" gorm:"type:int;not null"`
	State                  string `json:"state" gorm:"type:varchar(32);not null;index"`
	ReservedQuota          int    `json:"reserved_quota" gorm:"type:int;not null;default:0"`
	SettledQuota           int    `json:"settled_quota" gorm:"type:int;not null;default:0"`
	RefundedQuota          int    `json:"refunded_quota" gorm:"type:int;not null;default:0"`
	AnomalyQuota           int    `json:"anomaly_quota" gorm:"type:int;not null;default:0"`
	ModelName              string `json:"model_name" gorm:"type:varchar(191);not null;default:''"`
	ChannelId              int    `json:"channel_id" gorm:"type:int;not null;default:0;index"`
	CreatedAt              int64  `json:"created_at" gorm:"type:bigint;not null"`
	SettledAt              int64  `json:"settled_at" gorm:"type:bigint;not null;default:0"`
	RefundedAt             int64  `json:"refunded_at" gorm:"type:bigint;not null;default:0"`
}

type APIKeyDelivery struct {
	Id             int    `json:"id"`
	UserId         int    `json:"user_id" gorm:"type:int;not null;index"`
	TokenId        int    `json:"token_id" gorm:"type:int;not null;index"`
	Email          string `json:"email" gorm:"type:varchar(255);not null"`
	Status         string `json:"status" gorm:"type:varchar(32);not null;index"`
	IdempotencyKey string `json:"idempotency_key" gorm:"type:varchar(191);not null;uniqueIndex"`
	Attempts       int    `json:"attempts" gorm:"type:int;not null;default:0"`
	NextAttemptAt  int64  `json:"next_attempt_at" gorm:"type:bigint;not null;default:0"`
	LastErrorCode  string `json:"last_error_code" gorm:"type:varchar(64);not null;default:''"`
	CreatedAt      int64  `json:"created_at" gorm:"type:bigint;not null"`
	DeliveredAt    int64  `json:"delivered_at" gorm:"type:bigint;not null;default:0"`
}

var errEnterpriseOwnerConflict = errors.New("enterprise owner relationship conflicts with user anchor")

// MigrateEnterpriseFoundation applies enterprise schema in dependency order,
// then creates the initial Root/Admin enterprise relationships.
func MigrateEnterpriseFoundation() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	return migrateEnterpriseFoundation(DB)
}

func migrateEnterpriseFoundation(db *gorm.DB) error {
	for _, schema := range []any{
		&User{},
		&Enterprise{},
		&EnterpriseMembership{},
		&EnterpriseInvitation{},
		&EnterpriseLedger{},
		&EnterpriseUsageRecord{},
		&APIKeyDelivery{},
	} {
		if err := db.AutoMigrate(schema); err != nil {
			return err
		}
	}
	// Older drafts used request_id as a unique key. GORM does not remove an
	// obsolete unique index when a tag changes, so replace its generated index
	// with an ordinary index after AutoMigrate and retain only the
	// token/idempotency contract.
	const requestIDIndex = "idx_enterprise_usage_records_request_id"
	if db.Migrator().HasIndex(&EnterpriseUsageRecord{}, requestIDIndex) {
		if err := db.Migrator().DropIndex(&EnterpriseUsageRecord{}, requestIDIndex); err != nil {
			return err
		}
	}
	if !db.Migrator().HasIndex(&EnterpriseUsageRecord{}, requestIDIndex) {
		if err := db.Migrator().CreateIndex(&EnterpriseUsageRecord{}, requestIDIndex); err != nil {
			return err
		}
	}
	return ensurePlatformAdminEnterprises(db)
}

// EnsurePlatformAdminEnterprises is exported for controlled migration checks.
// It is idempotent: each Root/Admin receives at most one owner enterprise.
func EnsurePlatformAdminEnterprises() error {
	if DB == nil {
		return errors.New("database is not initialized")
	}
	return ensurePlatformAdminEnterprises(DB)
}

func ensurePlatformAdminEnterprises(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var users []User
		if err := lockForUpdate(tx).Where("role IN (?, ?)", common.RoleAdminUser, common.RoleRootUser).Order("id ASC").Find(&users).Error; err != nil {
			return err
		}
		for i := range users {
			if err := ensurePlatformAdminEnterprise(tx, &users[i]); err != nil {
				return fmt.Errorf("user %d: %w", users[i].Id, err)
			}
		}
		return nil
	})
}

// EnsurePlatformAdminEnterpriseWithTx creates or repairs the independent
// enterprise relationship for a newly-created or newly-promoted Root/Admin.
// The caller owns the transaction that writes the user's platform role.
func EnsurePlatformAdminEnterpriseWithTx(tx *gorm.DB, user *User) error {
	if tx == nil || user == nil || user.Id == 0 {
		return errors.New("invalid platform admin enterprise input")
	}
	var persisted User
	if err := lockForUpdate(tx).Where("id = ?", user.Id).First(&persisted).Error; err != nil {
		return err
	}
	if !isPlatformAdminRole(persisted.Role) {
		return nil
	}
	if err := ensurePlatformAdminEnterprise(tx, &persisted); err != nil {
		return err
	}
	user.ActiveEnterpriseId = persisted.ActiveEnterpriseId
	return nil
}

func ensurePlatformAdminEnterprise(tx *gorm.DB, user *User) error {
	var enterprise Enterprise
	if user.ActiveEnterpriseId != 0 {
		if err := tx.Where("id = ?", user.ActiveEnterpriseId).First(&enterprise).Error; err != nil {
			return fmt.Errorf("active enterprise %d: %w", user.ActiveEnterpriseId, err)
		}
		if enterprise.OwnerUserId != user.Id {
			return errEnterpriseOwnerConflict
		}
	} else {
		var owned []Enterprise
		if err := tx.Where("owner_user_id = ?", user.Id).Order("id ASC").Find(&owned).Error; err != nil {
			return err
		}
		if len(owned) > 1 {
			return errEnterpriseOwnerConflict
		}
		if len(owned) == 1 {
			enterprise = owned[0]
		} else {
			enterprise = Enterprise{
				Name:        enterpriseNameForUser(user),
				OwnerUserId: user.Id,
				Status:      EnterpriseStatusActive,
				CreatedAt:   common.GetTimestamp(),
				UpdatedAt:   common.GetTimestamp(),
			}
			if err := tx.Create(&enterprise).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&User{}).Where("id = ? AND active_enterprise_id = 0", user.Id).Update("active_enterprise_id", enterprise.Id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errEnterpriseOwnerConflict
		}
		user.ActiveEnterpriseId = enterprise.Id
	}
	if user.ActiveEnterpriseId == 0 {
		user.ActiveEnterpriseId = enterprise.Id
	}

	var memberships []EnterpriseMembership
	if err := tx.Where("enterprise_id = ? AND user_id = ?", enterprise.Id, user.Id).Order("id ASC").Find(&memberships).Error; err != nil {
		return err
	}
	if len(memberships) == 0 {
		membership := EnterpriseMembership{
			EnterpriseId: enterprise.Id,
			UserId:       user.Id,
			Role:         EnterpriseMembershipRoleOwner,
			Status:       EnterpriseMembershipStatusActive,
			JoinedAt:     common.GetTimestamp(),
		}
		return tx.Create(&membership).Error
	}
	if len(memberships) != 1 || memberships[0].Role != EnterpriseMembershipRoleOwner {
		return errEnterpriseOwnerConflict
	}
	return nil
}

func isPlatformAdminRole(role int) bool {
	return role == common.RoleRootUser || role == common.RoleAdminUser
}

func enterpriseNameForUser(user *User) string {
	if user.DisplayName != "" {
		return user.DisplayName + " Enterprise"
	}
	return user.Username + " Enterprise"
}
