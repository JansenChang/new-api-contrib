package service

import (
	"errors"
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// EnterpriseBillingSession is the synchronous relay adapter for one already
// reserved EnterpriseUsageRecord. It intentionally never touches the personal
// wallet, subscription, or token quota paths used by BillingSession.
type EnterpriseBillingSession struct {
	usageID          int64
	preConsumedQuota int
	submitted        bool
	settled          bool
	refunded         bool
	manualReview     bool
	mu               sync.Mutex
}

func NewEnterpriseBillingSession(outcome model.EnterpriseUsageOutcome) (*EnterpriseBillingSession, error) {
	if outcome.UsageID <= 0 || outcome.ReservedQuota <= 0 || !outcome.Execute {
		return nil, errors.New("invalid enterprise billing reservation")
	}
	return &EnterpriseBillingSession{
		usageID:          outcome.UsageID,
		preConsumedQuota: outcome.ReservedQuota,
	}, nil
}

// MarkUpstreamSubmitted persists the irreversible boundary before the relay
// handler may send bytes to an upstream provider.
func (s *EnterpriseBillingSession) MarkUpstreamSubmitted() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded || s.manualReview {
		return errors.New("enterprise billing session is terminal")
	}
	if s.submitted {
		return nil
	}
	if _, err := model.MarkEnterpriseUsageUpstreamSubmitted(s.usageID); err != nil {
		return err
	}
	s.submitted = true
	return nil
}

func (s *EnterpriseBillingSession) WasSubmitted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.submitted
}

func (s *EnterpriseBillingSession) SnapshotChannel(channelID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded || s.manualReview || s.submitted {
		return errors.New("enterprise billing session is not pending")
	}
	return model.SnapshotEnterpriseUsageChannel(s.usageID, channelID)
}

func (s *EnterpriseBillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	if s.refunded || s.manualReview {
		return errors.New("enterprise billing session is terminal")
	}
	if _, err := model.SettleEnterpriseUsage(s.usageID, actualQuota); err != nil {
		return err
	}
	s.settled = true
	return nil
}

// Refund is synchronous because it is part of the request's upstream-send
// proof boundary. Before submission a known local failure releases the exact
// reservation; after submission any result is treated as unknown and remains
// for manual review.
func (s *EnterpriseBillingSession) Refund(c *gin.Context) {
	if err := s.finishFailure(); err != nil {
		common.SysError(fmt.Sprintf("enterprise relay failure finalization error: %s", err.Error()))
		return
	}
	if c != nil {
		logger.LogInfo(c, "企业调用未完成，已按上游提交状态保留企业账务")
	}
}

func (s *EnterpriseBillingSession) finishFailure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded || s.manualReview {
		return nil
	}
	if s.submitted {
		if _, err := model.MarkEnterpriseUsageManualReview(s.usageID); err != nil {
			return err
		}
		s.manualReview = true
		return nil
	}
	if _, err := model.RefundEnterpriseUsage(s.usageID); err != nil {
		return err
	}
	s.refunded = true
	return nil
}

func (s *EnterpriseBillingSession) MarkManualReview() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled || s.refunded || s.manualReview {
		return nil
	}
	if _, err := model.MarkEnterpriseUsageManualReview(s.usageID); err != nil {
		return err
	}
	s.manualReview = true
	return nil
}

func (s *EnterpriseBillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.settled && !s.refunded && !s.manualReview
}

func (s *EnterpriseBillingSession) GetPreConsumedQuota() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preConsumedQuota
}

// Reserve is intentionally fail-closed. The initial enterprise reservation is
// immutable in this slice; a later routing attempt that needs more quota must
// not silently use the personal BillingSession path.
func (s *EnterpriseBillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if targetQuota <= s.preConsumedQuota {
		return nil
	}
	return errors.New("enterprise billing does not support increasing a reservation after creation")
}

func (s *EnterpriseBillingSession) IsTerminal() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settled || s.refunded || s.manualReview
}
