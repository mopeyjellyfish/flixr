package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartScanReturnsRandomnessFailure(t *testing.T) {
	original := readRandom
	readRandom = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	t.Cleanup(func() { readRandom = original })

	c := New()
	err := c.StartScan(context.Background(), 1)
	require.Error(t, err)
	assert.ErrorContains(t, err, "entropy unavailable")
	assert.Empty(t, c.ScanStatus().ID)
}
