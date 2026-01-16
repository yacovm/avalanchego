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
	m    sync.Map
	once sync.Once
}

func (fqs *FailedQueryStats) Inc(nodeID ids.NodeID) {
	fqs.once.Do(func() {
		go func() {
			fqs.printStats()
		}()
	})
	var count uint64
	loadedOrStored, _ := fqs.m.LoadOrStore(nodeID, &count)
	los := loadedOrStored.(*uint64)
	atomic.AddUint64(los, 1)
}

func (fqs *FailedQueryStats) printStats() {
	for {
		time.Sleep(time.Minute)
		nodeIDsToFailedQueries := make(map[ids.NodeID]uint64)
		fqs.m.Range(func(key, value interface{}) bool {
			v := value.(*uint64)
			nodeIDsToFailedQueries[key.(ids.NodeID)] = atomic.LoadUint64(v)
			return true
		})

		// Convert nodeIDsToFailedQueries to slice sorted by the number of failed queries
		type kv struct {
			Key   ids.NodeID
			Value uint64
		}
		var sorted []kv
		for k, v := range nodeIDsToFailedQueries {
			sorted = append(sorted, kv{k, v})
		}

		// Sort by Value in descending order
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Value > sorted[j].Value
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
			fmt.Printf("%s: %d\n", kv.Key, kv.Value)
		}
		fmt.Println("----------------------------------")
	}
}
