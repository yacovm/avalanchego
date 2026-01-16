// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/ava-labs/avalanchego/ids"
)

const (
	longHistorySize      = 120 // 2 minutes
	shortHistorySize     = 20
	benchInitialCapacity = 128
)

type stats struct {
	time      int64
	successes int
	failures  int
}

func (s *stats) incSuccesses() {
	s.successes++
}

func (s *stats) incFailures() {
	s.failures++
}

func (s *stats) timestamp() int64 {
	return s.time
}

func (s *stats) successesFailuresTime() (int, int, int64) {
	return s.successes, s.failures, s.time
}

func (s *stats) reset(time int64) {
	s.successes = 0
	s.failures = 0
	s.time = time
}

type lockedStats struct {
	lock  sync.Mutex
	stats stats
}

func (ls *lockedStats) markFailure(t time.Time) {
	ls.lock.Lock()
	defer ls.lock.Unlock()

	defer ls.stats.incFailures()

	timestamp := t.Unix()

	// If the timestamp has changed, we reset the time.
	if ls.stats.timestamp() != timestamp {
		ls.stats.reset(timestamp)
	}
}

func (ls *lockedStats) markSuccess(t time.Time) {
	ls.lock.Lock()
	defer ls.lock.Unlock()

	defer ls.stats.incSuccesses()

	timestamp := t.Unix()

	// If the timestamp has changed, we reset the time.
	if ls.stats.timestamp() != timestamp {
		ls.stats.reset(timestamp)
	}
}

func (ls *lockedStats) successesFailuresTime() (int, int, int64) {
	ls.lock.Lock()
	defer ls.lock.Unlock()
	return ls.stats.successesFailuresTime()
}

type benchStatus struct {
	history          [longHistorySize]lockedStats
	latestEvents     *failureStats
	benched          atomic.Bool
}

func (bs *benchStatus) isBenched() bool {
	return bs.benched.Load()
}

func (bs *benchStatus) bench() {
	bs.benched.Store(true)
}

func (bs *benchStatus) markFailure(t time.Time, timePeriod int64) {
	currentTime := t.Unix()
	longHistoryIndex := currentTime % longHistorySize
	bs.history[longHistoryIndex].markFailure(t)

	bs.latestEvents.markFailure(t)

	if successCount, failureCount := bs.successesFailuresTime(currentTime, timePeriod); successCount == 0 && failureCount > 0 {
		bs.bench()
	}
}

func (bs *benchStatus) markSuccess(t time.Time) {
	currentTime := t.Unix()

	longHistoryIndex := currentTime % longHistorySize
	bs.history[longHistoryIndex].markSuccess(t)

	bs.latestEvents.markFailure(t)
}

func (bs *benchStatus) successesFailuresTime(currentTime, timePeriod int64) (int, int) {
	// Sanity check
	if currentTime < timePeriod {
		return 0, 0
	}

	lowerTimestampBoundary := currentTime - timePeriod

	var successes, failures int
	for _, e := range bs.history {
		s, f, timestamp := e.successesFailuresTime()
		if timestamp < lowerTimestampBoundary {
			continue
		}
		successes += s
		failures += f
	}
	return successes, failures
}

type benchList struct {
	lock          sync.RWMutex
	benchedNodes  map[ids.NodeID]*benchStatus
	time          func() time.Time
	scanFrequency time.Duration
	close         chan struct{}
}

func NewBenchList() *benchList {
	bl := benchList{
		benchedNodes: make(map[ids.NodeID]*benchStatus, benchInitialCapacity),
	}

	go bl.run()

	return &bl
}

func (b *benchList) RegisterResponse(nodeID ids.NodeID) {
	// If the node is unknown to us, it has never failed before, so ignore.
	status, exists := b.maybeGetBenchedStatus(nodeID)
	if !exists {
		return
	}
	// Else, we know that the node has failed in the past, so register its success
	status.markSuccess(b.time())
}

func (b *benchList) RegisterFailure(nodeID ids.NodeID) {
	b.getOrCreateBenchedNode(nodeID).markFailure(b.time(), 0)

}

func (b *benchList) IsBenched(nodeID ids.NodeID) bool {
	bs, exists := b.maybeGetBenchedStatus(nodeID)
	if !exists {
		return false
	}
	return bs.isBenched()
}

func (b *benchList) run() {
	ticker := time.NewTicker(b.scanFrequency)
	for {
		select {
		case <-b.close:
			return
		case <-ticker.C:
			b.scan()
		}
	}
}

func (b *benchList) scan() {
	// We iterate over all benched nodes and see if the success frequency has improved or dropped below the threshold
	b.lock.Lock()
	defer b.lock.Unlock()
}

func (b *benchList) getOrCreateBenchedNode(nodeID ids.NodeID) *benchStatus {
	// Check optimistically first, maybe the node is already benched
	status, exists := b.maybeGetBenchedStatus(nodeID)
	if exists {
		return status
	}

	b.lock.Lock()
	defer b.lock.Unlock()
	// Check again under the lock, maybe someone beat us to it
	status, exists = b.benchedNodes[nodeID]
	if exists {
		return status
	}

	// We're the first ones to bench this node
	status = &benchStatus{}
	b.benchedNodes[nodeID] = status
	return status
}

func (b *benchList) maybeGetBenchedStatus(nodeID ids.NodeID) (*benchStatus, bool) {
	b.lock.RLock()
	status, exists := b.benchedNodes[nodeID]
	b.lock.RUnlock()
	if exists {
		return status, true
	}
	return nil, false
}
