package sessiondelivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// DeliveryTokenMetrics uses the final Anthropic delivery usage fields. The
// input_tokens field is the uncached tail; TotalInputTokens also includes
// cache creation and cache reads, so every delivered input token is counted
// exactly once.
type DeliveryTokenMetrics struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	TotalInputTokens         int64 `json:"total_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	TotalTokens              int64 `json:"total_tokens"`
	CountedDeliveries        int64 `json:"counted_deliveries"`
}

// TokenVolume is the payload exposed by the status API. Pending database
// records only guarantee total input/output, while archived batches retain
// the full final cache split.
type TokenVolume struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	TotalInputTokens         int64 `json:"total_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	TotalTokens              int64 `json:"total_tokens"`
	CountedDeliveries        int64 `json:"counted_deliveries"`
	UncountedDeliveries      int64 `json:"uncounted_deliveries"`
	BreakdownAvailable       bool  `json:"breakdown_available"`
}

func tokenVolumeFromPending(totalInput, output, counted, expected int64) (TokenVolume, error) {
	total, err := addTokenValues(totalInput, output)
	if err != nil {
		return TokenVolume{}, err
	}
	return TokenVolume{
		TotalInputTokens:    totalInput,
		OutputTokens:        output,
		TotalTokens:         total,
		CountedDeliveries:   counted,
		UncountedDeliveries: maxInt64(expected-counted, 0),
	}, nil
}

func tokenVolumeFromArchived(metrics DeliveryTokenMetrics, expected int64) (TokenVolume, error) {
	if err := metrics.Validate(); err != nil {
		return TokenVolume{}, err
	}
	return TokenVolume{
		InputTokens:              metrics.InputTokens,
		CacheCreationInputTokens: metrics.CacheCreationInputTokens,
		CacheReadInputTokens:     metrics.CacheReadInputTokens,
		TotalInputTokens:         metrics.TotalInputTokens,
		OutputTokens:             metrics.OutputTokens,
		TotalTokens:              metrics.TotalTokens,
		CountedDeliveries:        metrics.CountedDeliveries,
		UncountedDeliveries:      maxInt64(expected-metrics.CountedDeliveries, 0),
		BreakdownAvailable:       true,
	}, nil
}

func (m DeliveryTokenMetrics) Validate() error {
	values := []int64{
		m.InputTokens,
		m.CacheCreationInputTokens,
		m.CacheReadInputTokens,
		m.TotalInputTokens,
		m.OutputTokens,
		m.TotalTokens,
		m.CountedDeliveries,
	}
	for _, value := range values {
		if value < 0 {
			return errors.New("delivery token metrics must be non-negative")
		}
	}
	input, err := addTokenValues(m.InputTokens, m.CacheCreationInputTokens, m.CacheReadInputTokens)
	if err != nil {
		return err
	}
	if m.TotalInputTokens != input {
		return errors.New("delivery total_input_tokens is inconsistent")
	}
	total, err := addTokenValues(input, m.OutputTokens)
	if err != nil {
		return err
	}
	if m.TotalTokens != total {
		return errors.New("delivery total_tokens is inconsistent")
	}
	return nil
}

func (m *DeliveryTokenMetrics) Add(other DeliveryTokenMetrics) error {
	if m == nil {
		return errors.New("delivery token metrics destination is nil")
	}
	if err := other.Validate(); err != nil {
		return err
	}
	var err error
	m.InputTokens, err = addTokenValues(m.InputTokens, other.InputTokens)
	if err != nil {
		return err
	}
	m.CacheCreationInputTokens, err = addTokenValues(m.CacheCreationInputTokens, other.CacheCreationInputTokens)
	if err != nil {
		return err
	}
	m.CacheReadInputTokens, err = addTokenValues(m.CacheReadInputTokens, other.CacheReadInputTokens)
	if err != nil {
		return err
	}
	m.TotalInputTokens, err = addTokenValues(m.TotalInputTokens, other.TotalInputTokens)
	if err != nil {
		return err
	}
	m.OutputTokens, err = addTokenValues(m.OutputTokens, other.OutputTokens)
	if err != nil {
		return err
	}
	m.TotalTokens, err = addTokenValues(m.TotalTokens, other.TotalTokens)
	if err != nil {
		return err
	}
	m.CountedDeliveries, err = addTokenValues(m.CountedDeliveries, other.CountedDeliveries)
	return err
}

func ExtractDeliveryTokenMetrics(record *DeliveryRecord) (DeliveryTokenMetrics, error) {
	if record == nil {
		return DeliveryTokenMetrics{}, errors.New("delivery record is nil")
	}
	response, err := decodeJSONObject(record.Response.ResponseData, "response.response_data")
	if err != nil {
		return DeliveryTokenMetrics{}, err
	}
	usage, err := decodeJSONObject(response["usage"], "response.response_data.usage")
	if err != nil {
		return DeliveryTokenMetrics{}, err
	}
	input, err := decodeTokenInt64(usage["input_tokens"], "input_tokens")
	if err != nil {
		return DeliveryTokenMetrics{}, err
	}
	output, err := decodeTokenInt64(usage["output_tokens"], "output_tokens")
	if err != nil {
		return DeliveryTokenMetrics{}, err
	}
	cacheCreation, err := decodeOptionalTokenInt64(usage["cache_creation_input_tokens"], "cache_creation_input_tokens")
	if err != nil {
		return DeliveryTokenMetrics{}, err
	}
	cacheRead, err := decodeOptionalTokenInt64(usage["cache_read_input_tokens"], "cache_read_input_tokens")
	if err != nil {
		return DeliveryTokenMetrics{}, err
	}
	totalInput, err := addTokenValues(input, cacheCreation, cacheRead)
	if err != nil {
		return DeliveryTokenMetrics{}, err
	}
	total, err := addTokenValues(totalInput, output)
	if err != nil {
		return DeliveryTokenMetrics{}, err
	}
	metrics := DeliveryTokenMetrics{
		InputTokens:              input,
		CacheCreationInputTokens: cacheCreation,
		CacheReadInputTokens:     cacheRead,
		TotalInputTokens:         totalInput,
		OutputTokens:             output,
		TotalTokens:              total,
		CountedDeliveries:        1,
	}
	return metrics, metrics.Validate()
}

func decodeOptionalTokenInt64(raw json.RawMessage, name string) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	return decodeTokenInt64(raw, name)
}

func decodeTokenInt64(raw json.RawMessage, name string) (int64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("delivery usage is missing %s", name)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("delivery usage %s must be an integer", name)
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("delivery usage %s must be a non-negative integer", name)
	}
	return value, nil
}

func addTokenValues(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value < 0 || total > math.MaxInt64-value {
			return 0, errors.New("delivery token metrics overflow")
		}
		total += value
	}
	return total, nil
}
