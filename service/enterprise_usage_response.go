package service

import (
	"net/http"

	"github.com/QuantumNous/new-api/model"
)

// EnterpriseUsageReplayHTTPStatus is the fixed response contract for a
// duplicate enterprise idempotency key. It never represents a relay response.
func EnterpriseUsageReplayHTTPStatus(state string) int {
	switch state {
	case model.EnterpriseUsageStatePending, model.EnterpriseUsageStateUpstreamSubmitted:
		return http.StatusAccepted
	case model.EnterpriseUsageStateManualReview:
		return http.StatusConflict
	case model.EnterpriseUsageStateSettled, model.EnterpriseUsageStateRefunded, model.EnterpriseUsageStateAnomaly:
		return http.StatusOK
	default:
		return http.StatusConflict
	}
}
