package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type modelFamilyPolicyErrorWriter func(c *gin.Context, status int, errType, message string)

func rejectAPIKeyModelFamilyPolicy(
	c *gin.Context,
	apiKey *service.APIKey,
	model string,
	openAIEndpoint bool,
	writeError modelFamilyPolicyErrorWriter,
) bool {
	if apiKey == nil {
		return false
	}
	denied := apiKey.IsModelFamilyDenied(model)
	if openAIEndpoint {
		denied = apiKey.IsOpenAIEndpointDenied(model)
	}
	if !denied {
		return false
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	writeError(c, http.StatusForbidden, "permission_error", service.APIKeyModelAccessDeniedMessage)
	return true
}
