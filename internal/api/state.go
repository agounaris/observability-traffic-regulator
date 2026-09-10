package api

import (
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/argy/otel-traffic-regulator/internal/filter"
	"github.com/argy/otel-traffic-regulator/internal/series"
)

type Counters struct {
	Requests         atomic.Uint64
	SamplesReceived  atomic.Uint64
	SamplesForwarded atomic.Uint64
	SamplesDropped   atomic.Uint64
	UpstreamErrors   atomic.Uint64
}

type State struct {
	Gate     *filter.Gate
	Store    *series.Store
	Counters Counters
	Logger   *slog.Logger
	policy   atomic.Value // stores Policy
}

type Policy struct {
	AllowedPercentage uint32    `json:"allowed_percentage"`
	Version           uint64    `json:"version"`
	Origin            string    `json:"origin"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func NewState(store *series.Store, nodeID ...string) *State {
	return NewStateWithLogger(store, slog.Default(), nodeID...)
}

func NewStateWithLogger(store *series.Store, logger *slog.Logger, nodeID ...string) *State {
	origin := "local"
	if len(nodeID) > 0 && nodeID[0] != "" {
		origin = nodeID[0]
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &State{Gate: filter.NewGate(), Store: store, Logger: logger}
	// Version zero lets an existing peer policy win after a restart. A local
	// update gets a wall-clock version and then participates in normal ordering.
	s.policy.Store(Policy{AllowedPercentage: 100, Version: 0, Origin: origin, UpdatedAt: time.Now().UTC()})
	return s
}

func (s *State) CurrentPolicy() Policy { return s.policy.Load().(Policy) }

// SetLocalAllowed creates a newer policy version for this node.
func (s *State) SetLocalAllowed(allowed uint32) Policy {
	current := s.CurrentPolicy()
	version := uint64(time.Now().UnixNano())
	if version <= current.Version {
		version = current.Version + 1
	}
	policy := Policy{AllowedPercentage: allowed, Version: version, Origin: current.Origin, UpdatedAt: time.Now().UTC()}
	s.policy.Store(policy)
	s.Gate.SetAllowed(allowed)
	return policy
}

// MergePolicy applies a policy only when it is newer than the local policy.
// Origin breaks ties deterministically if two nodes update at the same time.
func (s *State) MergePolicy(incoming Policy) bool {
	if incoming.AllowedPercentage > 100 || incoming.Origin == "" {
		return false
	}
	current := s.CurrentPolicy()
	if incoming.Version < current.Version || (incoming.Version == current.Version && incoming.Origin <= current.Origin) {
		return false
	}
	s.policy.Store(incoming)
	s.Gate.SetAllowed(incoming.AllowedPercentage)
	return true
}

func (s *State) Observe(key string, now time.Time) { s.Store.Touch(key, now) }
