// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"sync"
	"time"
)

type event struct {
	time    time.Time
	success bool
}
type eventCircularBuffer struct {
	buffer []event
	index  int
}

func (cbc *eventCircularBuffer) push(time time.Time, success bool) {
	cbc.buffer[cbc.index] = event{time: time, success: success}
	cbc.index = (cbc.index + 1) % len(cbc.buffer)
}

func (cbc *eventCircularBuffer) peak() (time.Time, bool) {
	event := cbc.buffer[cbc.index]
	return event.time, event.success
}

func (cbc *eventCircularBuffer) oldest() (time.Time, bool) {
	index := (cbc.index + 1) % len(cbc.buffer)
	event := cbc.buffer[index]
	return event.time, event.success
}

func (cbc *eventCircularBuffer) forEach(f func (time.Time, bool)) {
	for i := 0; i < len(cbc.buffer); i++ {
		index := (cbc.index + i) % len(cbc.buffer)
		event := cbc.buffer[index]
		f(event.time, event.success)
	}
}

type failureStats struct {
	lock sync.Mutex
	cbc eventCircularBuffer
}

func NewFailureStats(size int) *failureStats {
	return &failureStats{cbc: eventCircularBuffer{buffer: make([]event, size)}}
}

func (fs *failureStats) markFailure(timestamp time.Time) () {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	fs.cbc.push(timestamp, false)
}

func (fs *failureStats) markSuccess(timestamp time.Time) {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	fs.cbc.push(timestamp, true)
}

func (fs *failureStats) hasFailedSuccessivelyWithinPeriod(period time.Duration) bool {
	fs.lock.Lock()
	defer fs.lock.Unlock()

	oldestFailure, failed := fs.cbc.oldest()
	if ! failed || oldestFailure.IsZero() {
		return false
	}

	latestFailure, failed := fs.cbc.peak()
	if ! failed || latestFailure.IsZero() {
		return false
	}

	if oldestFailure.Add(period).Before(latestFailure) {
		return false
	}

	var someSucceeded bool
	fs.cbc.forEach(func(timestamp time.Time, success bool) {
		if success {
			someSucceeded = true
		}
	})

	return ! someSucceeded
}
