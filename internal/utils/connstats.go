package utils

import "sync/atomic"

// ConnectionStats stores stats for the connection. See the public
// ConnectionStats struct in connection.go for more information
type ConnectionStats struct {
	BytesSent       atomic.Uint64
	PacketsSent     atomic.Uint64
	BytesReceived   atomic.Uint64
	PacketsReceived atomic.Uint64
	BytesLost       atomic.Uint64
	PacketsLost     atomic.Uint64

	// spec/001-loss-recovery C3
	CopiesSent         atomic.Uint64
	CopyBytesSent      atomic.Uint64
	CopiesSkipped      atomic.Uint64
	SpuriousLosses     atomic.Uint64
	PTOCount           atomic.Uint64
	DuplicatesReceived atomic.Uint64
}
