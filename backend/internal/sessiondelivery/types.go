package sessiondelivery

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	SchemaVersion      = 2
	DefaultPublicModel = "claude-opus-5"
)

type Protocol string

const (
	ProtocolAnthropicMessages Protocol = "anthropic_messages"
	ProtocolOpenAIResponses   Protocol = "openai_responses"
)

type Scope struct {
	UserID   int64 `json:"user_id,omitempty"`
	APIKeyID int64 `json:"api_key_id,omitempty"`
	GroupID  int64 `json:"group_id,omitempty"`
}

type SourceInfo struct {
	Protocol Protocol `json:"protocol"`
	Endpoint string   `json:"endpoint"`
	Scope    Scope    `json:"scope"`
	Stream   bool     `json:"stream"`
}

type OriginalPayload struct {
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response,omitempty"`
}

type Rejection struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Envelope is the internal, versioned capture object stored in the durable
// spool and the isolated Session database. It is never delivered directly to
// the external consumer.
type Envelope struct {
	SchemaVersion    int             `json:"schema_version"`
	RecordID         string          `json:"record_id"`
	SessionID        string          `json:"session_id"`
	RequestID        string          `json:"request_id"`
	OccurredAt       time.Time       `json:"occurred_at"`
	CapturedAt       time.Time       `json:"captured_at"`
	GatewayRequestID string          `json:"gateway_request_id"`
	Source           SourceInfo      `json:"source"`
	HTTPStatus       int             `json:"http_status"`
	DurationMS       int64           `json:"duration_ms"`
	Original         OriginalPayload `json:"original"`
	Delivery         *DeliveryRecord `json:"delivery,omitempty"`
	Rejection        *Rejection      `json:"rejection,omitempty"`
}

type DeliveryMetadata struct {
	HTTPStatus int    `json:"http_status"`
	LatencyMS  int64  `json:"latency_ms"`
	UserAgent  string `json:"user_agent,omitempty"`
}

// deliveryTimestampLayout is ISO8601 UTC with fixed millisecond precision, as
// in the vendor specification example. Go's default RFC3339Nano marshaling
// drops trailing zeros in the fraction, which produces variable-width
// timestamps that no API log emits.
const deliveryTimestampLayout = "2006-01-02T15:04:05.000Z"

// DeliveryTime marshals as a fixed-precision UTC instant while behaving as a
// time.Time everywhere else.
type DeliveryTime struct {
	time.Time
}

func (t DeliveryTime) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.Time.UTC().Format(deliveryTimestampLayout) + `"`), nil
}

func (t *DeliveryTime) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return err
	}
	t.Time = parsed.UTC()
	return nil
}

type DeliveryResponse struct {
	StatusCode   int             `json:"status_code,omitempty"`
	ResponseData json.RawMessage `json:"response_data,omitempty"`
	Error        json.RawMessage `json:"error,omitempty"`
}

// DeliveryRecord is intentionally limited to the fields accepted by the
// vendor delivery specification. Internal protocol, account, routing and
// upstream model information must stay in Envelope.Source/Original.
type DeliveryRecord struct {
	SessionID string           `json:"session_id"`
	RequestID string           `json:"request_id,omitempty"`
	Timestamp DeliveryTime     `json:"timestamp"`
	Metadata  DeliveryMetadata `json:"metadata"`
	Request   json.RawMessage  `json:"request"`
	Response  DeliveryResponse `json:"response"`
}

type CaptureInput struct {
	Protocol         Protocol
	Endpoint         string
	Scope            Scope
	GatewayRequestID string
	SessionHeader    string
	StartedAt        time.Time
	CompletedAt      time.Time
	HTTPStatus       int
	RequestBody      []byte
	ResponseBody     []byte
	MaxEventBytes    int
}

type RecorderConfig struct {
	Enabled         bool
	PublicModel     string
	HMACSecret      string
	SpoolDir        string
	SpoolMaxBytes   int64
	CaptureMaxBytes int64
}
