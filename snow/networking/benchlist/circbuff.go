// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
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
	// Sanity check: if the buffer is empty, return
	if len(cbc.buffer) == 0 {
		return
	}
	cbc.buffer[cbc.index] = event{time: time, success: success}
	cbc.index = (cbc.index + 1) % len(cbc.buffer)
}

func (cbc *eventCircularBuffer) latest() (time.Time, bool) {
	// Sanity check: if the buffer is empty, return the zero value
	if len(cbc.buffer) == 0 {
		return time.Time{}, false
	}
	index := (cbc.index + len(cbc.buffer) - 1) % len(cbc.buffer)
	event := cbc.buffer[index]
	return event.time, event.success
}

func (cbc *eventCircularBuffer) oldest() (time.Time, bool) {
	// Sanity check: if the buffer is empty, return the zero value
	if len(cbc.buffer) == 0 {
		return time.Time{}, false
	}

	index := cbc.index % len(cbc.buffer)
	event := cbc.buffer[index]
	return event.time, event.success
}

func (cbc *eventCircularBuffer) forEach(f func(time.Time, bool)) {
	// Sanity check: if the buffer is empty, return
	if len(cbc.buffer) == 0 {
		return
	}

	for i := 0; i < len(cbc.buffer); i++ {
		index := (cbc.index + i) % len(cbc.buffer)
		event := cbc.buffer[index]
		f(event.time, event.success)
	}
}
