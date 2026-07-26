package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsIncludeOnlyNamedGPT56Tiers(t *testing.T) {
	ids := DefaultModelIDs()
	require.Contains(t, ids, "gpt-5.6-sol")
	require.Contains(t, ids, "gpt-5.6-terra")
	require.Contains(t, ids, "gpt-5.6-luna")
	require.NotContains(t, ids, "gpt-5.6")
}
