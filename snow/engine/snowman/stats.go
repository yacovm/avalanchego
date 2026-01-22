// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package snowman

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/set"
)

type FailedQueryStats struct {
	lazyValidators atomic.Value
	m              sync.Map
	once           sync.Once
}

type queryStats struct {
	success  uint64
	failures uint64
	benched  atomic.Bool
}

func (fqs *FailedQueryStats) Failed(nodeID ids.NodeID, benched bool) {
	los := fqs.getQueryStats(nodeID)
	atomic.AddUint64(&los.failures, 1)
	los.benched.Store(benched)
}

func (fqs *FailedQueryStats) Succeeded(nodeID ids.NodeID) {
	los := fqs.getQueryStats(nodeID)
	atomic.AddUint64(&los.success, 1)
}

func (fqs *FailedQueryStats) getQueryStats(nodeID ids.NodeID) *queryStats {
	fqs.once.Do(func() {
		go func() {
			fqs.printStats()
		}()
	})
	var stats queryStats
	loadedOrStored, _ := fqs.m.LoadOrStore(nodeID, &stats)
	los := loadedOrStored.(*queryStats)
	return los
}

func (fqs *FailedQueryStats) printStats() {
	for {
		time.Sleep(time.Minute)
		nodeIDsToFailedQueries := make(map[ids.NodeID]uint64)
		nodeIDsToSuccessfulQueries := make(map[ids.NodeID]uint64)
		benched := make(map[ids.NodeID]bool)

		fqs.m.Range(func(key, value interface{}) bool {
			stats := value.(*queryStats)
			successes := atomic.LoadUint64(&stats.success)
			failures := atomic.LoadUint64(&stats.failures)
			if successes == 0 && failures == 0 {
				return true
			}
			nodeIDsToFailedQueries[key.(ids.NodeID)] = failures
			nodeIDsToSuccessfulQueries[key.(ids.NodeID)] = successes
			benched[key.(ids.NodeID)] = stats.benched.Load()
			return true
		})

		// Convert nodeIDsToFailedQueries to slice sorted by the number of failed queries
		type kv struct {
			Key          ids.NodeID
			FailureRatio int
			SuccessRatio int
			Benched      bool
		}
		var sorted []kv
		for k, v := range nodeIDsToFailedQueries {
			sorted = append(sorted, kv{Key: k, FailureRatio: int(v), SuccessRatio: int(nodeIDsToSuccessfulQueries[k]), Benched: benched[k]})
		}

		// Sort by Value in descending order
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].FailureRatio > sorted[j].FailureRatio
		})

		limit := min(len(sorted), 10)
		sorted = sorted[:limit]

		var lazy set.Set[ids.NodeID]
		for _, kv := range sorted {
			lazy.Add(kv.Key)
		}

		fqs.lazyValidators.Store(lazy)

		fmt.Println("----------------------------------")
		for _, kv := range sorted {
			fmt.Printf("%s: failures: %d, successes: %d, benched? %v\n", kv.Key, kv.FailureRatio, kv.SuccessRatio, kv.Benched)
		}
		fmt.Println("----------------------------------")
	}
}
