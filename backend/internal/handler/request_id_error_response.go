package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestid"
	"github.com/gin-gonic/gin"
)

func gatewayRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return requestid.FromRequest(c.Request)
}

func anthropicErrorBody(c *gin.Context, errType, message string) gin.H {
	body := gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	}
	if rid := gatewayRequestID(c); rid != "" {
		body["request_id"] = rid
	}
	return body
}

func openAIErrorBody(c *gin.Context, errType, message string) gin.H {
	errorObj := gin.H{
		"type":    errType,
		"message": message,
	}
	if rid := gatewayRequestID(c); rid != "" {
		errorObj["request_id"] = rid
	}
	return gin.H{"error": errorObj}
}

func responsesErrorBody(c *gin.Context, code, message string) gin.H {
	errorObj := gin.H{
		"code":    code,
		"message": message,
	}
	if rid := gatewayRequestID(c); rid != "" {
		errorObj["request_id"] = rid
	}
	return gin.H{"error": errorObj}
}
