// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLongTermStats(t *testing.T) {
	lts := newLongTermStats(120, time.Minute, time.Now, 0.1)
	require.False(t, lts.shouldBeBenched())
	now := time.Now()
	for i := 0; i <= 60; i++ {
		lts.markFailure(now)
		now = now.Add(time.Second)
		require.False(t, lts.shouldBeBenched())
	}

	lts.markFailure(now)
	require.True(t, lts.shouldBeBenched())
}
