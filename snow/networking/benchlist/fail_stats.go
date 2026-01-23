// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"sync"
	"time"
)

type failureStats struct {
	lock   sync.Mutex
	cbc    eventCircularBuffer
	period time.Duration
}

func NewFailureStats(size int, period time.Duration) *failureStats {
	return &failureStats{cbc: eventCircularBuffer{buffer: make([]event, size)}, period: period}
}

func (fs *failureStats) markFailure(timestamp time.Time) {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	fs.cbc.push(timestamp, false)
}

func (fs *failureStats) markSuccess(timestamp time.Time) {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	fs.cbc.push(timestamp, true)
}

func (fs *failureStats) shouldBeBenched() bool {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	oldestFailure, success := fs.cbc.oldest()
	if success || oldestFailure.IsZero() {
		return false
	}

	latestFailure, success := fs.cbc.latest()
	if success || latestFailure.IsZero() {
		return false
	}

	if oldestFailure.Add(fs.period).Before(latestFailure) {
		return false
	}

	var someSucceeded bool
	fs.cbc.forEach(func(timestamp time.Time, success bool) {
		if timestamp.IsZero() {
			return
		}
		if success {
			someSucceeded = true
		}
	})

	return !someSucceeded
}
