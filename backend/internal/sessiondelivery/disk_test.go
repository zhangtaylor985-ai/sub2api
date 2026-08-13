package sessiondelivery

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiskUsagePercent(t *testing.T) {
	usage, err := DiskUsagePercent(t.TempDir())
	require.NoError(t, err)
	require.GreaterOrEqual(t, usage, 0)
	require.LessOrEqual(t, usage, 100)

	_, err = DiskUsagePercent(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}
