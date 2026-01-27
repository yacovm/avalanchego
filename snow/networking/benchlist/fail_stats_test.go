// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFailureStats(t *testing.T) {
	fs := NewFailureStats(5, time.Second * 4)

	require.False(t, fs.shouldBeBenched())

	start := time.Now()
	for i := 0; i < 4; i++ {
		fs.markFailure(start.Add(time.Duration(i) * time.Second))
		require.False(t, fs.shouldBeBenched())
	}

	fs.markFailure(start.Add(time.Duration(4) * time.Second))
	require.True(t, fs.shouldBeBenched())

	fs.markSuccess(start.Add(time.Duration(5) * time.Second))
	require.False(t, fs.shouldBeBenched())

	for i := 0; i < 4; i++ {
		fs.markFailure(start.Add(time.Duration(i+6) * time.Second))
		require.False(t, fs.shouldBeBenched())
	}

	fs.markFailure(start.Add(time.Duration(10) * time.Second))
	require.True(t, fs.shouldBeBenched())

}
