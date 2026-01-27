// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCircularBuffer(t *testing.T) {
	var cb eventCircularBuffer
	timestamp, ok := cb.oldest()
	require.False(t, ok)
	require.Zero(t, timestamp)

	cb.buffer = make([]event, 10)

	var timestamps []time.Time

	start := time.Now()
	for i := 0; i < 10; i++ {
		timestamps = append(timestamps, start.Add(time.Duration(i)*time.Second))
	}

	for i := 0; i < 10; i++ {
		cb.push(timestamps[i], true)
		res, ok := cb.latest()
		require.True(t, ok)
		require.Equal(t, timestamps[i], res)

		timestamp, ok := cb.oldest()

		if i == 9 {
			require.True(t, ok)
			require.Equal(t, timestamps[0], timestamp)
			break
		}
		require.False(t, ok)
		require.Zero(t, timestamp)
	}

	var i int
	cb.forEach(func(timestamp time.Time, success bool) {
		require.Equal(t, timestamps[i], timestamp, i)
		i++
	})
}
