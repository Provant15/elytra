package gamebridge

import (
	"context"
	"encoding/json"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apex/log"
	"github.com/pyrohost/elytra/src/events"
)

const (
	// PollInterval is the base interval for polling player changes.
	// Actual interval includes +/-1s jitter to avoid thundering herd.
	PollInterval = 5 * time.Second

	// MaxBackoff is the maximum poll interval during error backoff.
	MaxBackoff = 30 * time.Second

	// Player event topic names published to the event bus.
	PlayerListEvent  = "player list"
	PlayerJoinEvent  = "player join"
	PlayerLeaveEvent = "player leave"
)

// Subscriber manages the RCON polling lifecycle for player list updates.
// It only polls when at least one websocket client is subscribed.
type Subscriber struct {
	bridge Bridge
	bus    *events.Bus
	log    *log.Entry

	subscribers atomic.Int32
	cancel      context.CancelFunc
	mu          sync.Mutex

	// cacheMu protects the cached player set from concurrent access
	// (poll goroutine vs stopPolling/UnsubscribeAll).
	cacheMu sync.Mutex

	// cached is the last known player set, keyed by player name.
	cached map[string]Player

	// consecutiveErrors tracks sequential poll failures for backoff.
	// Atomic to allow lock-free reads from currentInterval().
	consecutiveErrors atomic.Int32
}

// NewSubscriber creates a subscriber that emits player events to the given bus.
func NewSubscriber(bridge Bridge, bus *events.Bus, logger *log.Entry) *Subscriber {
	return &Subscriber{
		bridge: bridge,
		bus:    bus,
		log:    logger,
		cached: make(map[string]Player),
	}
}

// Subscribe increments the subscriber count and starts polling if this is the first.
func (s *Subscriber) Subscribe() {
	count := s.subscribers.Add(1)
	s.log.WithField("subscribers", count).Debug("player subscriber added")

	if count == 1 {
		s.startPolling()
	}
}

// Unsubscribe decrements the subscriber count and stops polling when zero.
func (s *Subscriber) Unsubscribe() {
	count := s.subscribers.Add(-1)
	if count < 0 {
		s.subscribers.Store(0)
		count = 0
	}
	s.log.WithField("subscribers", count).Debug("player subscriber removed")

	if count == 0 {
		s.stopPolling()
	}
}

// UnsubscribeAll removes all subscribers and stops polling.
func (s *Subscriber) UnsubscribeAll() {
	s.subscribers.Store(0)
	s.stopPolling()
}

func (s *Subscriber) startPolling() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		return // Already polling.
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	s.log.Info("starting player list polling")
	go s.pollLoop(ctx)
}

func (s *Subscriber) stopPolling() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
		s.cacheMu.Lock()
		s.cached = make(map[string]Player)
		s.cacheMu.Unlock()
		s.consecutiveErrors.Store(0)
		s.log.Info("stopped player list polling")
	}
}

func (s *Subscriber) pollLoop(ctx context.Context) {
	// Immediately do the first poll.
	s.poll()

	for {
		// Calculate interval with jitter (+/-1s) and error backoff.
		interval := s.currentInterval()
		jitter := time.Duration(rand.Int63n(int64(2*time.Second))) - time.Second
		timer := time.NewTimer(interval + jitter)

		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.poll()
		}
	}
}

// currentInterval returns the poll interval, applying exponential backoff on errors.
func (s *Subscriber) currentInterval() time.Duration {
	errs := s.consecutiveErrors.Load()
	if errs == 0 {
		return PollInterval
	}
	bo := PollInterval * time.Duration(1<<errs)
	if bo > MaxBackoff {
		bo = MaxBackoff
	}
	return bo
}

func (s *Subscriber) poll() {
	list, err := s.bridge.GetPlayers()

	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	if err != nil {
		s.consecutiveErrors.Add(1)
		s.log.WithError(err).WithField("backoff", s.currentInterval()).Warn("failed to poll player list, retaining cached state")
		// On error: do NOT diff, do NOT emit leave events. Keep prior snapshot.
		return
	}
	s.consecutiveErrors.Store(0)

	// Build new player set.
	newSet := make(map[string]Player, len(list.Players))
	for _, p := range list.Players {
		newSet[p.Name] = p
	}

	// Diff: find joins (in new but not cached).
	for name, p := range newSet {
		if _, existed := s.cached[name]; !existed {
			data, _ := json.Marshal(p)
			s.bus.Publish(PlayerJoinEvent, string(data))
		}
	}

	// Diff: find leaves (in cached but not new).
	for name, p := range s.cached {
		if _, exists := newSet[name]; !exists {
			data, _ := json.Marshal(p)
			s.bus.Publish(PlayerLeaveEvent, string(data))
		}
	}

	// Always emit the full list so subscribers have authoritative state.
	data, _ := json.Marshal(list)
	s.bus.Publish(PlayerListEvent, string(data))

	s.cached = newSet
}
