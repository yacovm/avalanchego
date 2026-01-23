// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	safemath "github.com/ava-labs/avalanchego/utils/math"
	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/utils/set"

)

type historyBasedBenching interface {
	markFailure(time.Time)
	markSuccess(time.Time)
	shouldBeBenched() bool
}

type averagerBasedBenching struct {
	unbenchThreshold float64
	avg              math.Averager
}

func (a *averagerBasedBenching) markFailure(t time.Time) {
	a.avg.Observe(0, t)
}

func (a *averagerBasedBenching) markSuccess(t time.Time) {
	a.avg.Observe(1, t)
}

func (a *averagerBasedBenching) shouldBeBenched() bool {
	return a.avg.Read() < a.unbenchThreshold
}

type nodeBenchStatus struct {
	chainID       ids.ID
	benchNotifier Benchable
	canBenchMore  func(ids.NodeID) bool
	nodeID        ids.NodeID
	history       historyBasedBenching
	latestEvents  historyBasedBenching
	avgBench      averagerBasedBenching
	benched       atomic.Bool
	canBench      func() bool
}

func newNodeBenchStatus(nodeID ids.NodeID,
	chainid ids.ID,
	benchNotifier Benchable,
	historySize int,
	shortPeriod,
	longPeriod time.Duration,
	getTime func() time.Time,
	canBenchMore func(id ids.NodeID) bool,
	maxFailureThreshold float64,
	canBench func() bool,
) *nodeBenchStatus {
	return &nodeBenchStatus{
		canBench:     canBench,
		canBenchMore:  canBenchMore,
		benchNotifier: benchNotifier,
		chainID:       chainid,
		nodeID:        nodeID,
		latestEvents:  NewFailureStats(historySize, shortPeriod),
		history:       newLongTermStats(historySize, longPeriod, getTime, maxFailureThreshold),
		avgBench: averagerBasedBenching{
			unbenchThreshold: 0.9,
			avg:              math.NewSyncAverager(math.NewAverager(1, 5*time.Second, getTime())),
		},
	}
}

func (bns *nodeBenchStatus) isBenched() bool {
	return bns.benched.Load()
}

func (bns *nodeBenchStatus) bench() {
	if !bns.canBenchMore(bns.nodeID) {
		fmt.Println(">>>> wanted to bench", bns.nodeID, "but can't bench any more stake")
		return
	}
	bns.benched.Store(true)
	fmt.Println(">>>> Benched", bns.nodeID, "for", bns.chainID)
}

func (bns *nodeBenchStatus) unbench() {
	bns.benched.Store(false)
	fmt.Println(">>>> Un-Benched", bns.nodeID, "for", bns.chainID)
}

func (bns *nodeBenchStatus) markFailure(t time.Time) {
	bns.history.markFailure(t)
	bns.latestEvents.markFailure(t)
}

func (bns *nodeBenchStatus) markSuccess(t time.Time) {
	bns.history.markSuccess(t)
	bns.latestEvents.markFailure(t)
}

func (bns *nodeBenchStatus) maybeBenchNode() bool {
	if ! bns.canBench() {
		return false
	}
	benchedByHistory := bns.history.shouldBeBenched()
	benchedByLatest := bns.latestEvents.shouldBeBenched()
	if benchedByHistory || benchedByLatest {
		fmt.Println(">>>> Benching", bns.nodeID, "for", bns.chainID, "because", benchedByHistory, benchedByLatest)
		bns.bench()
		return true
	}
	if benchedByHistory != benchedByLatest {
		fmt.Println(">>>>>", bns.history.shouldBeBenched(), bns.latestEvents.shouldBeBenched())
	}
	return false
}

func (bns *nodeBenchStatus) maybeUnbenchNode() bool {
	shouldNotBeBenchedByHistory := !bns.history.shouldBeBenched()
	shouldNotBeBenchedByLatest := !bns.latestEvents.shouldBeBenched()
	if shouldNotBeBenchedByHistory && shouldNotBeBenchedByLatest {
		fmt.Println(">>>> UnBenching", bns.nodeID, "for", bns.chainID, "because", shouldNotBeBenchedByHistory, shouldNotBeBenchedByLatest)
		bns.unbench()
		return true
	}
	if shouldNotBeBenchedByHistory != shouldNotBeBenchedByLatest {
		fmt.Println("<<<<<", !bns.history.shouldBeBenched(), !bns.latestEvents.shouldBeBenched())
	}
	return false
}

type BenchConfig struct {
	IsBootstrapping               func() bool
	ChainID                       ids.ID
	ScanFrequency                 time.Duration
	BenchInitialCapacity          int
	LongHistorySize               int
	ShortThresholdTimePeriod      time.Duration
	LongHistoryTimePeriod         time.Duration
	MaxAllowedBenchedStakePercent float64
	LongHistoryFailureThreshold   int
	Weight                        func(ids.NodeID) uint64
	SubsetWeight                  func(validatorIDs set.Set[ids.NodeID]) (uint64, error)
	TotalWeight                   func() (uint64, error)
	BenchNotifier                 Benchable
	Time                          func() time.Time
	Logger                        logging.Logger
	OnBenchedOrUnbench            func(n int, stake int64)
}

type benchList struct {
	config             BenchConfig
	lock               sync.RWMutex
	nodesToBenchStatus map[ids.NodeID]*nodeBenchStatus
	benchedNodes       atomic.Value // set.Set[ids.NodeID]
	close              chan struct{}
	prevScan           atomic.Value
}

func (bl *benchList) Benched(_ ids.ID, validatorID ids.NodeID) {
	bl.markNodeBenched(validatorID)
	bl.config.BenchNotifier.Benched(bl.config.ChainID, validatorID)
}

func (bl *benchList) Unbenched(_ ids.ID, validatorID ids.NodeID) {
	bl.markNodeUnbenched(validatorID)
	bl.config.BenchNotifier.Unbenched(bl.config.ChainID, validatorID)
}

func (bl *benchList) markNodeBenched(validatorID ids.NodeID) {
	val := bl.benchedNodes.Load()
	benchedNodes := val.(set.Set[ids.NodeID])
	benchedNodes.Add(validatorID)
	bl.benchedNodes.Store(benchedNodes)
}

func (bl *benchList) markNodeUnbenched(validatorID ids.NodeID) {
	val := bl.benchedNodes.Load()
	benchedNodes := val.(set.Set[ids.NodeID])
	benchedNodes.Remove(validatorID)
	bl.benchedNodes.Store(benchedNodes)
}

func NewBenchList(config BenchConfig) *benchList {
	bl := benchList{
		nodesToBenchStatus: make(map[ids.NodeID]*nodeBenchStatus, config.BenchInitialCapacity),
		config:             config,
		close:              make(chan struct{}),
	}
	bl.benchedNodes.Store(set.NewSet[ids.NodeID](0))
	bl.prevScan.Store(time.Now())

	return &bl
}

func (bl *benchList) newBenchStatus(nodeID ids.NodeID) *nodeBenchStatus {
	return newNodeBenchStatus(nodeID, bl.config.ChainID, bl, bl.config.LongHistorySize,
		bl.config.ShortThresholdTimePeriod,
		bl.config.LongHistoryTimePeriod,
		bl.config.Time,
		bl.canBenchMore,
		float64(bl.config.LongHistoryFailureThreshold)/100, func() bool {
			return ! bl.config.IsBootstrapping()
		})
}

func (bl *benchList) RegisterResponse(nodeID ids.NodeID) {
	bl.maybeScan()
	// If the node is unknown to us, it is not benched or in the process of being benched, so ignore.
	status, exists := bl.maybeGetBenchedStatus(nodeID)
	if !exists {
		return
	}
	// Else, we know that the node has failed in the past, so register its success
	status.markSuccess(bl.config.Time())
}

func (bl *benchList) RegisterFailure(nodeID ids.NodeID) {
	bl.maybeScan()
	bl.getOrCreateBenchedNode(nodeID).markFailure(bl.config.Time())
}

func (bl *benchList) IsBenched(nodeID ids.NodeID) bool {
	bs, exists := bl.maybeGetBenchedStatus(nodeID)
	if !exists {
		return false
	}
	return bs.isBenched()
}

func (bl *benchList) maybeScan() {
	now := bl.config.Time()
	prevScanTime := bl.prevScan.Load().(time.Time)
	if prevScanTime.Add(bl.config.ScanFrequency).Before(now) {
		bl.scan()
		bl.prevScan.Store(now)
	}
}

func (bl *benchList) stop() {
	select {
		case <-bl.close:
		default:
			close(bl.close)
	}
}

func (bl *benchList) scan() {
	bl.lock.Lock()
	defer bl.lock.Unlock()

	var shouldNotify bool

	var benchedCount int

	var unbenchedNodes []ids.NodeID
	var benchedNodes []ids.NodeID

	for _, benchStatus := range bl.nodesToBenchStatus {
		if benchStatus.isBenched() {
			unbenched := benchStatus.maybeUnbenchNode()
			if unbenched {
				unbenchedNodes = append(unbenchedNodes, benchStatus.nodeID)
			}
			shouldNotify = shouldNotify || unbenched
		} else {
			benched := benchStatus.maybeBenchNode()
			if benched {
				benchedNodes = append(benchedNodes, benchStatus.nodeID)
			}
			shouldNotify = shouldNotify || benched
		}

		if benchStatus.isBenched() {
			benchedCount++
		}
	}

	if shouldNotify {
		benchedStake, _, err := bl.computeBenchedStake()
		if err != nil {
			bl.config.Logger.Error("error calculating benched stake", zap.Error(err))
		} else {
			bl.config.OnBenchedOrUnbench(benchedCount, int64(benchedStake))
		}
	}

	go func() {
		for _, benchedNode := range benchedNodes {
			bl.Benched(bl.config.ChainID, benchedNode)
		}
		for _, unBenchedNode := range unbenchedNodes {
			bl.Unbenched(bl.config.ChainID, unBenchedNode)
		}
	}()
}

func (bl *benchList) getOrCreateBenchedNode(nodeID ids.NodeID) *nodeBenchStatus {
	// Check optimistically first, maybe the node is already benched
	status, exists := bl.maybeGetBenchedStatus(nodeID)
	if exists {
		return status
	}

	bl.lock.Lock()
	defer bl.lock.Unlock()
	// Check again under the lock, maybe someone beat us to it
	status, exists = bl.nodesToBenchStatus[nodeID]
	if exists {
		return status
	}

	// We're the first ones to bench this node
	status = bl.newBenchStatus(nodeID)
	bl.nodesToBenchStatus[nodeID] = status
	return status
}

func (bl *benchList) maybeGetBenchedStatus(nodeID ids.NodeID) (*nodeBenchStatus, bool) {
	bl.lock.RLock()
	status, exists := bl.nodesToBenchStatus[nodeID]
	bl.lock.RUnlock()
	if exists {
		return status, true
	}
	return nil, false
}

func (bl *benchList) canBenchMore(id ids.NodeID) bool {
	benchedStake, totalStake, err := bl.computeBenchedStake()
	if err != nil {
		bl.config.Logger.Error("error calculating benched stake", zap.Error(err))
		return false
	}

	nodeStake := bl.config.Weight(id)

	futureBenchedStake, err := safemath.Add(nodeStake, benchedStake)
	if err != nil {
		fmt.Println("node stake is", nodeStake * 100 / totalStake, "percent of total stake")
		bl.config.Logger.Error("overflow calculating future benched stake", zap.Error(err))
		return false
	}

	maxBenchedStake := float64(totalStake) * bl.config.MaxAllowedBenchedStakePercent

	if float64(futureBenchedStake) > maxBenchedStake {
		bl.config.Logger.Debug("cannot bench further nodes",
			zap.String("reason", "benched stake would exceed max allowed benched stake"),
			zap.Float64("benchedStake", float64(benchedStake)),
			zap.Float64("nodeStake", float64(nodeStake)),
			zap.Float64("maxBenchedStake", maxBenchedStake),
		)
		return false
	}
	return true
}

func (bl *benchList) computeBenchedStake() (uint64, uint64, error) {
	benchedVal := bl.benchedNodes.Load()
	if benchedVal == nil {
		return 0, 0, nil
	}

	benchedNodes := benchedVal.(set.Set[ids.NodeID])

	fmt.Println(">>>> Total", benchedNodes.Len(), "nodes are benched")

	benchedStake, err := bl.config.SubsetWeight(benchedNodes)
	if err != nil {
		bl.config.Logger.Error("error calculating benched stake",
			zap.Stringer("chainID", bl.config.ChainID),
			zap.Error(err),
		)
		return 0, 0, fmt.Errorf("error calculating benched stake: %w", err)
	}

	totalStake, err := bl.config.TotalWeight()
	if err != nil {
		bl.config.Logger.Error("error calculating total stake",
			zap.Stringer("chainID", bl.config.ChainID),
			zap.Error(err),
		)
		return 0, 0, fmt.Errorf("error calculating total stake: %w", err)
	}
	return benchedStake, totalStake, nil
}
