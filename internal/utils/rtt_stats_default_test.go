package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// spec/001-loss-recovery C1 InitialRTT.
func TestRTTStatsDefaultRTT(t *testing.T) {
	var unset RTTStats
	require.Equal(t, 2*DefaultInitialRTT, unset.PTO(false))

	rttStats := NewRTTStats()
	rttStats.SetDefaultRTT(20 * time.Millisecond)
	require.Equal(t, 20*time.Millisecond, rttStats.MinRTT())
	require.Equal(t, 20*time.Millisecond, rttStats.LatestRTT())
	require.Equal(t, 20*time.Millisecond, rttStats.SmoothedRTT())
	require.Equal(t, 40*time.Millisecond, rttStats.PTO(true))
	require.Equal(t, 40*time.Millisecond, rttStats.Clone().PTO(true))

	rttStats.UpdateRTT(7*time.Millisecond, 0)
	require.Equal(t, 7*time.Millisecond, rttStats.SmoothedRTT())
	rttStats.SetDefaultRTT(30 * time.Millisecond) // a sample is never replaced
	require.Equal(t, 7*time.Millisecond, rttStats.SmoothedRTT())

	rttStats.ResetForPathMigration()
	require.Equal(t, 30*time.Millisecond, rttStats.SmoothedRTT())
	require.Equal(t, 30*time.Millisecond, rttStats.MinRTT())
	require.Equal(t, 60*time.Millisecond, rttStats.PTO(false))
}
