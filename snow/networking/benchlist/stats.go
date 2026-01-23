// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"time"
)

type stats struct {
	val atomicTriple
}

func (s *stats) incSuccesses() {
	s.val.incA()
}

func (s *stats) incFailures() {
	s.val.incB()
}

func (s *stats) timestamp() int64 {
	_, _, timestamp := s.val.load()
	return int64(timestamp)
}

func (s *stats) successesFailuresTime() (int, int, int64) {
	success, failure, timestamp := s.val.load()
	return int(success), int(failure), int64(timestamp)
}

func (s *stats) reset(time int64) {
	s.val.store(0, 0, uint32(time))
}

func (s *stats) markFailure(t time.Time) {
	defer s.incFailures()

	timestamp := t.Unix()

	// If the timestamp has changed, we reset the time.
	if s.timestamp() != timestamp {
		s.reset(timestamp)
	}
}

func (s *stats) markSuccess(t time.Time) {
	defer s.incSuccesses()

	timestamp := t.Unix()

	// If the timestamp has changed, we reset the time.
	if s.timestamp() != timestamp {
		s.reset(timestamp)
	}
}

type longTermStats struct {
	history          []stats
	period           time.Duration
	failureThreshold float64
	getTime          func() time.Time
}

func newLongTermStats(size int, period time.Duration, getTime func() time.Time, failureThreshold float64) *longTermStats {
	return &longTermStats{
		failureThreshold: failureThreshold,
		getTime:          getTime,
		history:          make([]stats, size),
		period:           period,
	}
}

func (lts *longTermStats) markFailure(t time.Time) {
	currentTime := t.Unix()
	longHistoryIndex := currentTime % int64(len(lts.history))
	lts.history[longHistoryIndex].markFailure(t)
}

func (lts *longTermStats) markSuccess(t time.Time) {
	currentTime := t.Unix()

	longHistoryIndex := currentTime % int64(len(lts.history))
	lts.history[longHistoryIndex].markSuccess(t)
}

func (lts *longTermStats) shouldBeBenched() bool {
	lowestTimestampTakenInAccount := lts.getTime().Add(lts.period * time.Duration(-1)).Unix()

	var successes, failures int
	for _, e := range lts.history {
		s, f, timestamp := e.successesFailuresTime()
		if timestamp == 0 || timestamp < lowestTimestampTakenInAccount {
			continue
		}
		successes += s
		failures += f
	}

	denominator := failures + successes

	if denominator == 0 {
		return false
	}

	return float64(failures)/float64(denominator) > lts.failureThreshold
}
