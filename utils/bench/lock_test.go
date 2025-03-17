// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bench_test

import (
	"github.com/ava-labs/avalanchego/utils/bench"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

func TestLock(t *testing.T) {
	durations := make(chan time.Duration, 2)
	// Create a new lock
	l := bench.NewLock(&sync.Mutex{}, func(duration time.Duration) {
		durations <- duration
	})

	// Lock the lock
	l.Lock()

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		l.Lock()
		time.Sleep(time.Millisecond * 50)
		l.Unlock()
	}()

	time.Sleep(time.Millisecond * 5)

	// Unlock the lock
	l.Unlock()

	wg.Wait()

	close(durations)

	d1 := <-durations
	require.True(t, d1 > time.Millisecond*5)
	d2 := <-durations
	require.True(t, d2 > time.Millisecond*50)
}
