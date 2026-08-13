package handler

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requestid"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type openAIWSSessionCapture struct {
	recorder         *sessiondelivery.Recorder
	scope            sessiondelivery.Scope
	gatewayRequestID string
	sessionHeader    string

	mu       sync.Mutex
	requests map[int][]byte
	started  map[int]time.Time
}

func newOpenAIWSSessionCapture(c *gin.Context, subject middleware2.AuthSubject) *openAIWSSessionCapture {
	recorder, ok := middleware2.GetSessionDeliveryRecorder(c)
	if !ok {
		return nil
	}
	return &openAIWSSessionCapture{
		recorder: recorder,
		scope: sessiondelivery.Scope{
			UserID:   subject.UserID,
			APIKeyID: subject.APIKeyID,
			GroupID:  subject.GroupID,
		},
		gatewayRequestID: requestid.FromRequest(c.Request),
		sessionHeader:    openAIWSSessionHeader(c.Request),
		requests:         make(map[int][]byte),
		started:          make(map[int]time.Time),
	}
}

func (capture *openAIWSSessionCapture) remember(turn int, payload []byte) {
	if capture == nil || turn <= 0 || len(payload) == 0 || !capture.recorder.CanCapture() {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if _, exists := capture.requests[turn]; exists {
		return
	}
	capture.requests[turn] = append([]byte(nil), payload...)
	capture.started[turn] = time.Now().UTC()
}

func (capture *openAIWSSessionCapture) finish(
	turn int,
	result *service.OpenAIForwardResult,
	turnErr error,
	log *zap.Logger,
) {
	if capture == nil || turn <= 0 {
		return
	}
	capture.mu.Lock()
	requestBody := capture.requests[turn]
	startedAt := capture.started[turn]
	delete(capture.requests, turn)
	delete(capture.started, turn)
	capture.mu.Unlock()
	if len(requestBody) == 0 {
		return
	}
	if !capture.recorder.CanCapture() {
		return
	}

	status := http.StatusOK
	responseBody := []byte("data: {}\n\n")
	if turnErr != nil || result == nil {
		status = http.StatusBadGateway
	} else if len(result.WSTerminalPayload) > 0 {
		responseBody = make([]byte, 0, len(result.WSTerminalPayload)+8)
		responseBody = append(responseBody, "data: "...)
		responseBody = append(responseBody, result.WSTerminalPayload...)
		responseBody = append(responseBody, '\n', '\n')
	}
	completedAt := time.Now().UTC()
	if startedAt.IsZero() {
		startedAt = completedAt
		if result != nil && result.Duration > 0 {
			startedAt = completedAt.Add(-result.Duration)
		}
	}
	gatewayRequestID := strings.TrimSpace(capture.gatewayRequestID)
	if gatewayRequestID != "" {
		gatewayRequestID += "-ws-turn-" + strconv.Itoa(turn)
	}
	_, envelope, err := capture.recorder.Record(sessiondelivery.CaptureInput{
		Protocol:         sessiondelivery.ProtocolOpenAIResponses,
		Endpoint:         "/v1/responses",
		Scope:            capture.scope,
		GatewayRequestID: gatewayRequestID,
		SessionHeader:    capture.sessionHeader,
		StartedAt:        startedAt,
		CompletedAt:      completedAt,
		HTTPStatus:       status,
		RequestBody:      requestBody,
		ResponseBody:     responseBody,
	})
	if err != nil {
		if log != nil {
			log.Error("Session delivery WebSocket spool write failed", zap.Int("turn", turn), zap.Error(err))
		}
		return
	}
	if envelope != nil && envelope.Rejection != nil && log != nil {
		log.Warn(
			"Session delivery WebSocket record quarantined",
			zap.Int("turn", turn),
			zap.String("record_id", envelope.RecordID),
			zap.String("rejection_code", envelope.Rejection.Code),
		)
	}
}

func openAIWSSessionHeader(request *http.Request) string {
	if request == nil {
		return ""
	}
	for _, name := range []string{
		"X-Session-ID",
		"X-Conversation-ID",
		"Session-ID",
		"session_id",
		"conversation_id",
	} {
		if value := strings.TrimSpace(request.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}
