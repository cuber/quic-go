package quic

import (
	"errors"
	"fmt"
	"time"

	"github.com/apernet/quic-go/internal/ackhandler"
	"github.com/apernet/quic-go/internal/protocol"
	"github.com/apernet/quic-go/internal/utils"
)

// Defaults and limits of the loss recovery fields (spec/001-loss-recovery C1).
const (
	defaultAckElicitingThreshold = 2
	defaultLossTimeThreshold     = 9.0 / 8
	defaultLossPacketThreshold   = 3

	maxConfigAckDelay        = protocol.MaxMaxAckDelay - protocol.TimerGranularity // the advertised value adds the granularity
	maxConfigInitialRTT      = 10 * time.Second
	maxAckElicitingThreshold = 64
	maxPTOBackoffLimit       = 16
	minLossTimeThreshold     = 1.0
	maxLossTimeThreshold     = 4.0
	maxLossPacketThreshold   = 64
	maxDuplicateDelay        = time.Second
)

// LossRecovery holds the loss recovery and redundancy parameters a connection uses:
// the Config's fields, with an unset field replaced by quic-go's value.
type LossRecovery struct {
	MaxAckDelay           time.Duration
	AckElicitingThreshold int
	InitialRTT            time.Duration
	PTOBackoffLimit       int // 0: the PTO doubles without limit
	LossTimeThreshold     float64
	LossPacketThreshold   int
	DuplicateMaxSize      uint16 // 0: no size-based copies
	DuplicateTail         bool
	DuplicateDelay        time.Duration
}

// LossRecovery returns the loss recovery parameters a connection built from c uses.
// It may be called on a nil Config.
func (c *Config) LossRecovery() LossRecovery {
	r := LossRecovery{
		MaxAckDelay:           protocol.MaxAckDelay,
		AckElicitingThreshold: defaultAckElicitingThreshold,
		InitialRTT:            utils.DefaultInitialRTT,
		LossTimeThreshold:     defaultLossTimeThreshold,
		LossPacketThreshold:   defaultLossPacketThreshold,
	}
	if c == nil {
		return r
	}
	if c.MaxAckDelay != 0 {
		r.MaxAckDelay = c.MaxAckDelay
	}
	if c.AckElicitingThreshold != 0 {
		r.AckElicitingThreshold = c.AckElicitingThreshold
	}
	if c.InitialRTT != 0 {
		r.InitialRTT = c.InitialRTT
	}
	r.PTOBackoffLimit = c.PTOBackoffLimit
	if c.LossTimeThreshold != 0 {
		r.LossTimeThreshold = c.LossTimeThreshold
	}
	if c.LossPacketThreshold != 0 {
		r.LossPacketThreshold = c.LossPacketThreshold
	}
	r.DuplicateMaxSize = c.DuplicateMaxSize
	r.DuplicateTail = c.DuplicateTail
	r.DuplicateDelay = c.DuplicateDelay
	return r
}

func validateLossRecovery(c *Config) error {
	switch {
	case c.MaxAckDelay < 0 || c.MaxAckDelay > maxConfigAckDelay:
		return fmt.Errorf("invalid MaxAckDelay: %s (maximum %s)", c.MaxAckDelay, maxConfigAckDelay)
	case c.MaxAckDelay != 0 && c.MaxAckDelay < time.Millisecond:
		return fmt.Errorf("invalid MaxAckDelay: %s (minimum 1ms)", c.MaxAckDelay)
	case c.AckElicitingThreshold < 0 || c.AckElicitingThreshold > maxAckElicitingThreshold:
		return fmt.Errorf("invalid AckElicitingThreshold: %d (maximum %d)", c.AckElicitingThreshold, maxAckElicitingThreshold)
	case c.InitialRTT < 0 || c.InitialRTT > maxConfigInitialRTT:
		return fmt.Errorf("invalid InitialRTT: %s (maximum %s)", c.InitialRTT, maxConfigInitialRTT)
	case c.InitialRTT != 0 && c.InitialRTT < time.Millisecond:
		return fmt.Errorf("invalid InitialRTT: %s (minimum 1ms)", c.InitialRTT)
	case c.PTOBackoffLimit < 0 || c.PTOBackoffLimit > maxPTOBackoffLimit:
		return fmt.Errorf("invalid PTOBackoffLimit: %d (maximum %d)", c.PTOBackoffLimit, maxPTOBackoffLimit)
	case c.LossTimeThreshold != 0 && !(c.LossTimeThreshold >= minLossTimeThreshold && c.LossTimeThreshold <= maxLossTimeThreshold):
		return fmt.Errorf("invalid LossTimeThreshold: %g (from %g to %g)", c.LossTimeThreshold, minLossTimeThreshold, maxLossTimeThreshold)
	case c.LossPacketThreshold < 0 || c.LossPacketThreshold > maxLossPacketThreshold:
		return fmt.Errorf("invalid LossPacketThreshold: %d (maximum %d)", c.LossPacketThreshold, maxLossPacketThreshold)
	case c.DuplicateDelay < 0 || c.DuplicateDelay > maxDuplicateDelay:
		return fmt.Errorf("invalid DuplicateDelay: %s (maximum %s)", c.DuplicateDelay, maxDuplicateDelay)
	case c.DuplicateDelay != 0 && c.DuplicateMaxSize == 0 && !c.DuplicateTail:
		return errors.New("DuplicateDelay requires DuplicateMaxSize or DuplicateTail")
	}
	return nil
}

// ackhandlerRecovery is the part of r the sent packet handler applies.
func (r LossRecovery) ackhandlerRecovery() ackhandler.Recovery {
	return ackhandler.Recovery{
		PTOBackoffLimit:     r.PTOBackoffLimit,
		LossTimeThreshold:   r.LossTimeThreshold,
		LossPacketThreshold: r.LossPacketThreshold,
	}
}
