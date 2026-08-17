package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type enterpriseQuotaRequest struct {
	Quota *int `json:"quota"`
}

func SetEnterpriseOwner(c *gin.Context) {
	targetID, err := positivePathID(c, "id")
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	result, err := service.SetEnterpriseOwner(targetID)
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	recordManageAuditFor(c, targetID, "enterprise.owner_assign", map[string]interface{}{
		"user_id":       result.UserID,
		"enterprise_id": result.EnterpriseID,
		"membership_id": result.MembershipID,
	})
	common.ApiSuccess(c, result)
}

func GetEnterpriseSelf(c *gin.Context) {
	result, err := service.GetEnterpriseSelf(c.GetInt("id"))
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func ListEnterpriseMembers(c *gin.Context) {
	page := common.GetPageQuery(c)
	items, total, err := service.ListEnterpriseMembers(c.GetInt("id"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

func ListEnterpriseLedger(c *gin.Context) {
	page := enterpriseReadPage(c)
	items, total, err := service.ListEnterpriseLedger(c.GetInt("id"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

func ListEnterpriseUsage(c *gin.Context) {
	page := enterpriseReadPage(c)
	items, total, err := service.ListEnterpriseUsage(c.GetInt("id"), page.GetStartIdx(), page.GetPageSize())
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	page.SetTotal(int(total))
	page.SetItems(items)
	common.ApiSuccess(c, page)
}

func enterpriseReadPage(c *gin.Context) *common.PageInfo {
	page := common.GetPageQuery(c)
	if page.Page < 1 {
		page.Page = 1
	}
	if page.PageSize <= 0 || page.PageSize > 100 {
		page.PageSize = common.ItemsPerPage
	}
	return page
}

func AllocateEnterpriseQuota(c *gin.Context) {
	writeEnterpriseQuotaChange(c, "allocate")
}

func ReclaimEnterpriseQuota(c *gin.Context) {
	writeEnterpriseQuotaChange(c, "reclaim")
}

func writeEnterpriseQuotaChange(c *gin.Context, operation string) {
	membershipID, err := positivePathID(c, "id")
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	requestKey := c.GetHeader("Idempotency-Key")
	if len(requestKey) < 1 || len(requestKey) > 128 {
		writeEnterpriseError(c, service.ErrEnterpriseIdempotencyKeyInvalid)
		return
	}
	var request enterpriseQuotaRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || request.Quota == nil {
		writeEnterpriseError(c, model.ErrEnterpriseQuotaInvalid)
		return
	}
	if *request.Quota <= 0 || *request.Quota > common.MaxQuota {
		writeEnterpriseError(c, model.ErrEnterpriseQuotaInvalid)
		return
	}
	actorID := c.GetInt("id")
	var result model.EnterpriseMoneyResult
	if operation == "allocate" {
		result, err = service.AllocateEnterpriseQuota(actorID, membershipID, *request.Quota, requestKey)
	} else {
		result, err = service.ReclaimEnterpriseQuota(actorID, membershipID, *request.Quota, requestKey)
	}
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	recordManageAudit(c, "enterprise.quota_"+operation, map[string]interface{}{
		"membership_id": membershipID,
		"amount":        *request.Quota,
		"ledger_id":     result.LedgerID,
		"replayed":      result.Replayed,
	})
	common.ApiSuccess(c, result)
}

func PauseEnterpriseMember(c *gin.Context) {
	changeEnterpriseMemberStatus(c, "pause")
}

func ResumeEnterpriseMember(c *gin.Context) {
	changeEnterpriseMemberStatus(c, "resume")
}

func changeEnterpriseMemberStatus(c *gin.Context, operation string) {
	membershipID, err := positivePathID(c, "id")
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	actorID := c.GetInt("id")
	var projection model.EnterpriseMemberProjection
	if operation == "pause" {
		projection, err = service.PauseEnterpriseMember(actorID, membershipID)
	} else {
		projection, err = service.ResumeEnterpriseMember(actorID, membershipID)
	}
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	recordManageAudit(c, "enterprise.member_"+operation, map[string]interface{}{
		"membership_id": membershipID,
		"status":        projection.Status,
	})
	common.ApiSuccess(c, projection)
}

func RemoveEnterpriseMember(c *gin.Context) {
	membershipID, err := positivePathID(c, "id")
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	projection, pending, err := service.RemoveEnterpriseMember(c.GetInt("id"), membershipID)
	if err != nil {
		writeEnterpriseError(c, err)
		return
	}
	recordManageAudit(c, "enterprise.member_remove", map[string]interface{}{
		"membership_id":   membershipID,
		"status":          projection.Status,
		"removal_pending": pending,
	})
	common.ApiSuccess(c, gin.H{"member": projection, "removal_pending": pending})
}

func positivePathID(c *gin.Context, name string) (int, error) {
	id, err := strconv.Atoi(c.Param(name))
	if err != nil || id <= 0 {
		return 0, model.ErrEnterpriseMembershipNotFound
	}
	return id, nil
}

func writeEnterpriseError(c *gin.Context, err error) {
	status, code, message := enterpriseHTTPError(err)
	if status >= http.StatusInternalServerError && err != nil {
		common.SysLog("enterprise management error: " + err.Error())
	}
	c.AbortWithStatusJSON(status, gin.H{"success": false, "code": code, "message": message})
}

func enterpriseHTTPError(err error) (int, string, string) {
	switch {
	case errors.Is(err, service.ErrEnterpriseIdempotencyKeyInvalid):
		return http.StatusBadRequest, "ENTERPRISE_IDEMPOTENCY_KEY_INVALID", "invalid idempotency key"
	case errors.Is(err, model.ErrEnterpriseOwnerTargetInvalid):
		return http.StatusBadRequest, "ENTERPRISE_OWNER_TARGET_INVALID", "enterprise owner target is invalid"
	case errors.Is(err, model.ErrEnterpriseMembershipConflict):
		return http.StatusConflict, "ENTERPRISE_MEMBERSHIP_CONFLICT", "enterprise membership conflicts with an existing relationship"
	case errors.Is(err, model.ErrEnterpriseManagementRequired):
		return http.StatusForbidden, "ENTERPRISE_MEMBERSHIP_REQUIRED", "enterprise membership is required"
	case errors.Is(err, model.ErrEnterpriseOwnerRequired):
		return http.StatusForbidden, "ENTERPRISE_OWNER_REQUIRED", "enterprise owner permission is required"
	case errors.Is(err, model.ErrEnterpriseQuotaInvalid), errors.Is(err, model.ErrInvalidQuotaAmount):
		return http.StatusBadRequest, "ENTERPRISE_QUOTA_INVALID", "invalid enterprise quota"
	case errors.Is(err, model.ErrEnterpriseInsufficientQuota):
		return http.StatusConflict, "ENTERPRISE_INSUFFICIENT_QUOTA", "enterprise quota is insufficient"
	case errors.Is(err, model.ErrEnterpriseIdempotencyConflict):
		return http.StatusConflict, "ENTERPRISE_IDEMPOTENCY_CONFLICT", "idempotency key conflicts with a previous operation"
	case errors.Is(err, model.ErrEnterpriseMembershipNotFound), errors.Is(err, gorm.ErrRecordNotFound):
		return http.StatusNotFound, "ENTERPRISE_MEMBER_NOT_FOUND", "enterprise member was not found"
	case errors.Is(err, model.ErrEnterpriseInvalidMembershipState):
		return http.StatusConflict, "ENTERPRISE_INVALID_MEMBER_STATE", "enterprise member is in an invalid state"
	case errors.Is(err, model.ErrEnterpriseMembershipRemovalPending):
		return http.StatusConflict, "ENTERPRISE_REMOVAL_PENDING", "enterprise member removal is pending"
	default:
		return http.StatusInternalServerError, "ENTERPRISE_INTERNAL_ERROR", "enterprise operation failed"
	}
}
