//go:build unit

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestResolveEndpointColumn(t *testing.T) {
	tests := []struct {
		endpointType string
		want         string
	}{
		{"inbound", "ul.inbound_endpoint"},
		{"upstream", "ul.upstream_endpoint"},
		{"path", "ul.inbound_endpoint || ' -> ' || ul.upstream_endpoint"},
		{"", "ul.inbound_endpoint"},        // default
		{"unknown", "ul.inbound_endpoint"}, // fallback
	}

	for _, tc := range tests {
		t.Run(tc.endpointType, func(t *testing.T) {
			got := resolveEndpointColumn(tc.endpointType)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveModelDimensionExpression(t *testing.T) {
	requestedExpr := "COALESCE(NULLIF(TRIM(split_part(model_mapping_chain, '→', 1)), ''), NULLIF(TRIM(requested_model), ''), model)"
	tests := []struct {
		modelType string
		want      string
	}{
		{usagestats.ModelSourceRequested, requestedExpr},
		{usagestats.ModelSourceUpstream, "COALESCE(NULLIF(TRIM(upstream_model), ''), " + requestedExpr + ")"},
		{usagestats.ModelSourceMapping, "(" + requestedExpr + " || ' -> ' || COALESCE(NULLIF(TRIM(upstream_model), ''), " + requestedExpr + "))"},
		{"", requestedExpr},
		{"invalid", requestedExpr},
	}

	for _, tc := range tests {
		t.Run(tc.modelType, func(t *testing.T) {
			got := resolveModelDimensionExpression(tc.modelType)
			require.Equal(t, tc.want, got)
		})
	}
}
