package middleware

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestid"
	"github.com/Wei-Shaw/sub2api/internal/sessiondelivery"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var errSessionCaptureTooLarge = errors.New("Session capture exceeded configured byte limit")

const sessionDeliveryRecorderContextKey = "session_delivery_recorder"

type SessionCapturePolicy interface {
	ShouldCapture(apiKeyID int64) bool
}

func SessionDelivery(recorder *sessiondelivery.Recorder, policies ...SessionCapturePolicy) gin.HandlerFunc {
	if recorder == nil || !recorder.Enabled() {
		return func(c *gin.Context) { c.Next() }
	}
	var policy SessionCapturePolicy
	if len(policies) > 0 {
		policy = policies[0]
	}
	return func(c *gin.Context) {
		if policy != nil {
			subject, ok := GetAuthSubjectFromContext(c)
			if !ok || !policy.ShouldCapture(subject.APIKeyID) {
				c.Next()
				return
			}
		}
		if sessionCaptureWebSocketRoute(c) {
			if !recorder.CanCapture() {
				c.Next()
				return
			}
			c.Set(sessionDeliveryRecorderContextKey, recorder)
			c.Next()
			return
		}
		protocol, endpoint, eligible := sessionCaptureRoute(c)
		if !eligible {
			c.Next()
			return
		}
		if !recorder.CanCapture() {
			c.Next()
			return
		}

		requestFile, err := recorder.NewCaptureFile("request")
		if err != nil {
			logger.FromContext(c.Request.Context()).Error("Session delivery request capture unavailable", zap.Error(err))
			c.Next()
			return
		}
		responseFile, err := recorder.NewCaptureFile("response")
		if err != nil {
			_ = requestFile.Close()
			_ = os.Remove(requestFile.Name())
			logger.FromContext(c.Request.Context()).Error("Session delivery response capture unavailable", zap.Error(err))
			c.Next()
			return
		}
		requestPath := requestFile.Name()
		responsePath := responseFile.Name()
		originalBody := c.Request.Body
		if originalBody == nil {
			originalBody = http.NoBody
		}
		requestCapture := &bestEffortCaptureReadCloser{
			source: originalBody,
			target: requestFile,
			limit:  recorder.CaptureMaxBytes(),
		}
		c.Request.Body = requestCapture
		responseCapture := &bestEffortCaptureWriter{
			ResponseWriter: c.Writer,
			target:         responseFile,
			limit:          recorder.CaptureMaxBytes(),
		}
		originalWriter := c.Writer
		c.Writer = responseCapture
		restoreWriter := func() {
			if c.Writer == responseCapture {
				c.Writer = originalWriter
			}
		}
		defer restoreWriter()
		startedAt := time.Now().UTC()
		sessionHeader := firstSessionHeader(c)

		c.Next()

		completedAt := time.Now().UTC()
		httpStatus := responseCapture.Status()
		restoreWriter()
		_ = requestFile.Close()
		_ = responseFile.Close()
		if requestCapture.captureErr != nil || responseCapture.captureErr != nil {
			_ = os.Remove(requestPath)
			_ = os.Remove(responsePath)
			logger.FromContext(c.Request.Context()).Error(
				"Session delivery temporary capture failed",
				zap.Error(firstError(requestCapture.captureErr, responseCapture.captureErr)),
			)
			return
		}
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.APIKeyID <= 0 {
			_ = os.Remove(requestPath)
			_ = os.Remove(responsePath)
			return
		}
		_, envelope, err := recorder.RecordFiles(sessiondelivery.CaptureInput{
			Protocol:         protocol,
			Endpoint:         endpoint,
			Scope:            sessiondelivery.Scope{UserID: subject.UserID, APIKeyID: subject.APIKeyID, GroupID: subject.GroupID},
			GatewayRequestID: requestid.FromRequest(c.Request),
			SessionHeader:    sessionHeader,
			StartedAt:        startedAt,
			CompletedAt:      completedAt,
			HTTPStatus:       httpStatus,
		}, requestPath, responsePath)
		if err != nil {
			logger.FromContext(c.Request.Context()).Error("Session delivery spool write failed", zap.Error(err))
			return
		}
		if envelope != nil && envelope.Rejection != nil {
			logger.FromContext(c.Request.Context()).Warn(
				"Session delivery record quarantined",
				zap.String("record_id", envelope.RecordID),
				zap.String("rejection_code", envelope.Rejection.Code),
			)
		}
	}
}

func GetSessionDeliveryRecorder(c *gin.Context) (*sessiondelivery.Recorder, bool) {
	if c == nil {
		return nil, false
	}
	value, exists := c.Get(sessionDeliveryRecorderContextKey)
	if !exists {
		return nil, false
	}
	recorder, ok := value.(*sessiondelivery.Recorder)
	return recorder, ok && recorder != nil && recorder.Enabled()
}

func sessionCaptureWebSocketRoute(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.Method != http.MethodGet {
		return false
	}
	path := strings.TrimRight(c.Request.URL.Path, "/")
	switch path {
	case "/v1/responses", "/responses", "/openai/v1/responses", "/backend-api/codex/responses":
		return true
	default:
		return false
	}
}

func sessionCaptureRoute(c *gin.Context) (sessiondelivery.Protocol, string, bool) {
	if c == nil || c.Request == nil || c.Request.Method != "POST" {
		return "", "", false
	}
	path := strings.TrimRight(c.Request.URL.Path, "/")
	switch path {
	case "/v1/messages":
		return sessiondelivery.ProtocolAnthropicMessages, "/v1/messages", true
	case "/v1/responses", "/responses", "/openai/v1/responses", "/backend-api/codex/responses":
		return sessiondelivery.ProtocolOpenAIResponses, "/v1/responses", true
	default:
		return "", "", false
	}
}

func firstSessionHeader(c *gin.Context) string {
	for _, name := range []string{"X-Session-ID", "X-Conversation-ID", "Session-ID"} {
		if value := strings.TrimSpace(c.GetHeader(name)); value != "" {
			return value
		}
	}
	return ""
}

type bestEffortCaptureReadCloser struct {
	source     io.ReadCloser
	target     io.Writer
	limit      int64
	captured   int64
	captureErr error
}

func (r *bestEffortCaptureReadCloser) Read(buffer []byte) (int, error) {
	count, readErr := r.source.Read(buffer)
	if count > 0 && r.captureErr == nil {
		if r.limit <= 0 || int64(count) > r.limit-r.captured {
			r.captureErr = errSessionCaptureTooLarge
		} else {
			written, err := r.target.Write(buffer[:count])
			if err != nil {
				r.captureErr = err
			} else if written != count {
				r.captureErr = io.ErrShortWrite
			} else {
				r.captured += int64(written)
			}
		}
	}
	return count, readErr
}

func (r *bestEffortCaptureReadCloser) Close() error {
	return r.source.Close()
}

type bestEffortCaptureWriter struct {
	gin.ResponseWriter
	target     io.Writer
	limit      int64
	captured   int64
	captureErr error
}

func (w *bestEffortCaptureWriter) Write(value []byte) (int, error) {
	written, err := w.ResponseWriter.Write(value)
	if written >= 0 && written <= len(value) {
		w.capture(value[:written])
	}
	return written, err
}

func (w *bestEffortCaptureWriter) WriteString(value string) (int, error) {
	written, err := w.ResponseWriter.WriteString(value)
	if written >= 0 && written <= len(value) {
		w.capture([]byte(value[:written]))
	}
	return written, err
}

func (w *bestEffortCaptureWriter) capture(value []byte) {
	if len(value) == 0 || w.captureErr != nil {
		return
	}
	if w.limit <= 0 || int64(len(value)) > w.limit-w.captured {
		w.captureErr = errSessionCaptureTooLarge
		return
	}
	written, err := w.target.Write(value)
	if err != nil {
		w.captureErr = err
	} else if written != len(value) {
		w.captureErr = io.ErrShortWrite
	} else {
		w.captured += int64(written)
	}
}

func firstError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
