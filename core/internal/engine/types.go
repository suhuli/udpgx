package engine

import (
	"context"
	"github.com/suhuli/udpgx/core/internal/config"
	"github.com/suhuli/udpgx/core/internal/link"
	"sync/atomic"
	"time"
)

type Device struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Role      string           `json:"role"`
	Services  []config.Service `json:"services"`
	UpdatedAt int64            `json:"updatedAt"`
	Version   string           `json:"version"`
}
type LogEntry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}
type PeerView struct {
	DeviceID   string  `json:"deviceId"`
	Name       string  `json:"name"`
	State      string  `json:"state"`
	Path       string  `json:"path"`
	Local      string  `json:"local"`
	Remote     string  `json:"remote"`
	RTTMs      float64 `json:"rttMs"`
	LastError  string  `json:"lastError"`
	Streams    int64   `json:"streams"`
	Diagnostic string  `json:"diagnostic"`
	SetupMS    int64   `json:"setupMs"`
}
type MappingView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	State     string `json:"state"`
	LastError string `json:"lastError"`
}
type State struct {
	Running           bool          `json:"running"`
	BuiltinSignal     bool          `json:"builtinSignal"`
	SignalOnline      bool          `json:"signalOnline"`
	SignalError       string        `json:"signalError"`
	DeviceID          string        `json:"deviceId"`
	Name              string        `json:"name"`
	Role              string        `json:"role"`
	Version           string        `json:"version"`
	ServiceMode       bool          `json:"serviceMode"`
	Peers             []PeerView    `json:"peers"`
	Mappings          []MappingView `json:"mappings"`
	Devices           []Device      `json:"devices"`
	Logs              []LogEntry    `json:"logs"`
	BytesSent         uint64        `json:"bytesSent"`
	BytesReceived     uint64        `json:"bytesReceived"`
	ActiveConnections int64         `json:"activeConnections"`
	UptimeSeconds     int64         `json:"uptimeSeconds"`
}
type peerSlot struct {
	id, name, state, lastError string
	peer                       *link.Peer
	ready                      chan struct{}
	retryAt                    time.Time
	failures                   int
	incomingSession            string
	activeSession              string
	attemptCancel              context.CancelFunc
	cachedAnswer               *Signal
	diagnostic                 string
	started                    time.Time
	setupMs                    int64
	streams                    atomic.Int64
}
type Signal struct {
	ID          string           `json:"id"`
	Type        string           `json:"type"`
	Session     string           `json:"session"`
	From        string           `json:"from"`
	FromName    string           `json:"fromName"`
	To          string           `json:"to"`
	CreatedAt   int64            `json:"createdAt"`
	Description link.Description `json:"description"`
	Error       string           `json:"error,omitempty"`
}
