package quic

import (
	"math"
	"testing"
	"time"

	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/utils"

	"github.com/stretchr/testify/require"
)

// Tests for spec/001-loss-recovery C1.

func TestLossRecoveryDefaults(t *testing.T) {
	want := LossRecovery{
		MaxAckDelay:           protocol.MaxAckDelay,
		AckElicitingThreshold: 2,
		InitialRTT:            utils.DefaultInitialRTT,
		LossTimeThreshold:     9.0 / 8,
		LossPacketThreshold:   3,
	}
	var nilConfig *Config
	require.Equal(t, want, nilConfig.LossRecovery())
	require.Equal(t, want, (&Config{}).LossRecovery())
	require.Equal(t, want, populateConfig(nil).LossRecovery())
	// the defaults are what upstream advertises
	require.Equal(t, protocol.MaxAckDelayInclGranularity, want.MaxAckDelay+protocol.TimerGranularity)
}

func TestLossRecoverySet(t *testing.T) {
	c := populateConfig(&Config{
		MaxAckDelay:           2 * time.Millisecond,
		AckElicitingThreshold: 1,
		InitialRTT:            15 * time.Millisecond,
		PTOBackoffLimit:       2,
		LossTimeThreshold:     1.25,
		LossPacketThreshold:   6,
		DuplicateMaxSize:      300,
		DuplicateTail:         true,
		DuplicateDelay:        4 * time.Millisecond,
	})
	require.NoError(t, validateConfig(c))
	require.Equal(t, LossRecovery{
		MaxAckDelay:           2 * time.Millisecond,
		AckElicitingThreshold: 1,
		InitialRTT:            15 * time.Millisecond,
		PTOBackoffLimit:       2,
		LossTimeThreshold:     1.25,
		LossPacketThreshold:   6,
		DuplicateMaxSize:      300,
		DuplicateTail:         true,
		DuplicateDelay:        4 * time.Millisecond,
	}, c.LossRecovery())
}

func TestLossRecoveryValidation(t *testing.T) {
	valid := []*Config{
		{MaxAckDelay: time.Millisecond},
		{MaxAckDelay: 16382 * time.Millisecond},
		{AckElicitingThreshold: 64},
		{InitialRTT: time.Millisecond},
		{InitialRTT: 10 * time.Second},
		{PTOBackoffLimit: 16},
		{LossTimeThreshold: 1},
		{LossTimeThreshold: 4},
		{LossPacketThreshold: 64},
		{DuplicateMaxSize: math.MaxUint16},
		{DuplicateTail: true, DuplicateDelay: time.Second},
		{DuplicateMaxSize: 1, DuplicateDelay: time.Millisecond},
	}
	for _, c := range valid {
		require.NoError(t, validateConfig(c), "%+v", c.LossRecovery())
	}
	invalid := map[string]*Config{
		"MaxAckDelay":           {MaxAckDelay: 16383 * time.Millisecond},
		"MaxAckDelay ":          {MaxAckDelay: time.Microsecond},
		"MaxAckDelay  ":         {MaxAckDelay: -time.Millisecond},
		"AckElicitingThreshold": {AckElicitingThreshold: 65},
		"InitialRTT":            {InitialRTT: 10*time.Second + 1},
		"InitialRTT ":           {InitialRTT: time.Microsecond},
		"PTOBackoffLimit":       {PTOBackoffLimit: 17},
		"PTOBackoffLimit ":      {PTOBackoffLimit: -1},
		"LossTimeThreshold":     {LossTimeThreshold: 0.99},
		"LossTimeThreshold ":    {LossTimeThreshold: 4.01},
		"LossTimeThreshold  ":   {LossTimeThreshold: math.NaN()},
		"LossPacketThreshold":   {LossPacketThreshold: 65},
		"DuplicateDelay":        {DuplicateTail: true, DuplicateDelay: time.Second + 1},
		"DuplicateDelay ":       {DuplicateDelay: time.Millisecond},
	}
	for name, c := range invalid {
		require.Error(t, validateConfig(c), name)
	}
}

func TestLossRecoveryAppliedToConnection(t *testing.T) {
	conf := &Config{
		DisablePathMTUDiscovery: true,
		MaxAckDelay:             3 * time.Millisecond,
		AckElicitingThreshold:   1,
		InitialRTT:              12 * time.Millisecond,
	}
	for _, tc := range []*testConnection{
		newServerTestConnection(t, nil, conf, false),
		newClientTestConnection(t, nil, conf, false),
	} {
		require.Equal(t, conf.LossRecovery(), tc.conn.recovery)
		require.Equal(t, 24*time.Millisecond, tc.conn.rttStats.PTO(false))
	}
	tc := newClientTestConnection(t, nil, nil, false)
	require.Equal(t, 2*utils.DefaultInitialRTT, tc.conn.rttStats.PTO(false))
}
