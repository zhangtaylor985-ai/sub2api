package sessiondelivery

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractDeliveryTokenMetricsCountsCacheExactlyOnce(t *testing.T) {
	record := &DeliveryRecord{Response: DeliveryResponse{ResponseData: json.RawMessage(`{
		"usage":{
			"input_tokens":2,
			"cache_creation_input_tokens":120,
			"cache_read_input_tokens":880,
			"output_tokens":40
		}
	}`)}}
	metrics, err := ExtractDeliveryTokenMetrics(record)
	require.NoError(t, err)
	require.Equal(t, int64(1002), metrics.TotalInputTokens)
	require.Equal(t, int64(1042), metrics.TotalTokens)
	require.Equal(t, int64(1), metrics.CountedDeliveries)
	require.NoError(t, metrics.Validate())
}

func TestDeliveryTokenMetricsRejectsMissingNegativeAndOverflow(t *testing.T) {
	_, err := ExtractDeliveryTokenMetrics(&DeliveryRecord{Response: DeliveryResponse{ResponseData: json.RawMessage(`{"usage":{"output_tokens":1}}`)}})
	require.ErrorContains(t, err, "input_tokens")

	_, err = ExtractDeliveryTokenMetrics(&DeliveryRecord{Response: DeliveryResponse{ResponseData: json.RawMessage(`{"usage":{"input_tokens":1,"output_tokens":-1}}`)}})
	require.ErrorContains(t, err, "non-negative")

	metrics := DeliveryTokenMetrics{
		InputTokens: math.MaxInt64, TotalInputTokens: math.MaxInt64,
		OutputTokens: 0, TotalTokens: math.MaxInt64, CountedDeliveries: 1,
	}
	require.NoError(t, metrics.Validate())
	require.Error(t, metrics.Add(DeliveryTokenMetrics{
		InputTokens: 1, TotalInputTokens: 1, TotalTokens: 1, CountedDeliveries: 1,
	}))
}
