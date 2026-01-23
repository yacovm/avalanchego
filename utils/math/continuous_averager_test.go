// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package math

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestF(t *testing.T) {
	now := time.Now()

	a := NewAverager(1, time.Second * 20, now)

	for i := 0; i < 120; i++ {
		now = now.Add(time.Second)
		a.Observe(1, now)
	}

	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		a.Observe(0, now)
	}

	fmt.Println(a.Read()) // 0.5

	a = NewAverager(1, time.Second * 20, now)

	for i := 0; i < 120; i++ {
		now = now.Add(time.Second)
		a.Observe(0, now)
	}

	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		a.Observe(1, now)
	}

	fmt.Println(a.Read()) // 0.5
}

func TestAverager(t *testing.T) {
	require := require.New(t)

	halflife := time.Second
	currentTime := time.Now()

	a := NewSyncAverager(NewAverager(0, halflife, currentTime))
	require.Zero(a.Read())

	currentTime = currentTime.Add(halflife)
	a.Observe(1, currentTime)
	require.Equal(1.0/1.5, a.Read())
}

func TestAveragerTimeTravel(t *testing.T) {
	require := require.New(t)

	halflife := time.Second
	currentTime := time.Now()

	a := NewSyncAverager(NewAverager(1, halflife, currentTime))
	require.Equal(float64(1), a.Read())

	currentTime = currentTime.Add(-halflife)
	a.Observe(0, currentTime)
	require.Equal(1.0/1.5, a.Read())
}

func TestUninitializedAverager(t *testing.T) {
	require := require.New(t)

	halfLife := time.Second
	currentTime := time.Now()

	firstObservation := float64(10)

	a := NewUninitializedAverager(halfLife)
	require.Zero(a.Read())

	a.Observe(firstObservation, currentTime)
	require.Equal(firstObservation, a.Read())
}
