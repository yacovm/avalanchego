// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"fmt"
	"testing"
	"time"

	"github.com/ava-labs/avalanchego/utils/math"
)

func TestComputeHalfLife(t *testing.T) {
	halfLife, threshold := FindHalfLife()
	if halfLife == 0 {
		t.Fatal("Could not find halfLife")
	}
	fmt.Println(halfLife, threshold)
}

func FindHalfLife() (time.Duration, float64) {
	n := 120
	for i := 1; i < n; i += 1 {
		halfLife := time.Duration(i) * time.Second
		if threshold, found := evaluateHalfLife(halfLife); found {
			return halfLife, threshold
		}
	}

	return 0, 0
}

func evaluateHalfLife(halfLife time.Duration) (float64, bool) {
	criteria := []struct {
		name            string
		benchedOnStart  bool
		successes       []bool
		shouldBeBenched bool
	}{
		{
			name:            "Only failures",
			successes:       []bool{false, false, false, false, false, false, false, false, false, false, false, false},
			shouldBeBenched: true,
		},
		{
			name:            "Failed Recovery I",
			benchedOnStart:  true,
			successes:       []bool{true, true, true, true, true, true, true, true, true, false, false, false},
			shouldBeBenched: true,
		},
		{
			name:            "Alternating Success and Failure",
			successes:       []bool{true, true, false, true, false, true, false, true, false, true, false, true},
			shouldBeBenched: true,
		},
		{
			name:            "Occasional failure",
			successes:       []bool{true, true, false, true, true, false, true, true, false, true, true, false},
			shouldBeBenched: true,
		},
		/*		{
				name:            "Occasional failure II",
				successes:       []bool{true, true, true, false, true, true, false, true, true, false, true, true},
				shouldBeBenched: true,
			},*/
		{
			name:           "Successful Recovery I",
			benchedOnStart: true,
			successes:      []bool{true, true, true, true, true, true, true, true, false, true, false, true},
		},
		{
			name:           "Successful Recovery II",
			benchedOnStart: true,
			successes:      []bool{false, false, false, true, true, true, true, true, true, true, true, true},
		},
		{
			name:           "Successful Recovery III",
			benchedOnStart: true,
			successes:      []bool{true, true, false, true, true, true, false, true, true, false, true, true},
		},
		/*		{
				name:           "Successful Recovery IV",
				benchedOnStart: true,
				successes:      []bool{true, true, true, true, true, true, true, true, true, true, false, false},
			},*/
		{
			name:           "Only successes",
			benchedOnStart: true,
			successes:      []bool{true, true, true, true, true, true, true, true, true, true, true, true},
		},
	}

	resultsAndExpectedBenchStatus := make([]struct {
		result          float64
		shouldBeBenched bool
	}, 0, len(criteria))

	for _, schedule := range criteria {
		now := time.Now()
		var initVal float64 = 1
		if schedule.benchedOnStart {
			initVal = 0
		}
		a := math.NewAverager(initVal, halfLife, now)
		for i := 0; i < len(schedule.successes); i++ {
			var val float64
			if schedule.successes[i] {
				val = 1
			}
			a.Observe(val, now)
			now = now.Add(10 * time.Second)
		}

		result := a.Read()
		resultsAndExpectedBenchStatus = append(resultsAndExpectedBenchStatus, struct {
			result          float64
			shouldBeBenched bool
		}{result: result, shouldBeBenched: schedule.shouldBeBenched})
	}

	// We now find a threshold that satisfies all conditions

	for threshold := 0.7; threshold < 1; threshold += 0.05 {

		var foundConflict bool
		for _, entry := range resultsAndExpectedBenchStatus {
			if entry.shouldBeBenched && entry.result > threshold {
				// fmt.Println(">>>>", halfLife, criteria[j].name, entry.result, entry.shouldBeBenched)
				foundConflict = true
			}
			if !entry.shouldBeBenched && entry.result < threshold {
				// fmt.Println("<<<<", halfLife, criteria[j].name, entry.result, entry.shouldBeBenched)
				foundConflict = true
			}
		}

		if !foundConflict {
			return threshold, true
		}
	}

	return 0, false
}
