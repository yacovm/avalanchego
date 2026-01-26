// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/utils/set"

	safemath "github.com/ava-labs/avalanchego/utils/math"
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

func (a *averagerBasedBenching) successRate() float64 {
	return a.avg.Read()
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
	history       *averagerBasedBenching
	latestEvents  historyBasedBenching
	benched       atomic.Bool
	canBench      func() bool
}

func newNodeBenchStatus(nodeID ids.NodeID,
	chainid ids.ID,
	historySize int,
	shortPeriod time.Duration,
	getTime func() time.Time,
) *nodeBenchStatus {
	return &nodeBenchStatus{
		chainID:      chainid,
		nodeID:       nodeID,
		latestEvents: NewFailureStats(historySize, shortPeriod),
		history: &averagerBasedBenching{
			unbenchThreshold: 0.7,
			avg:              math.NewSyncAverager(math.NewAverager(1, 29*time.Second, getTime())),
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
	return newNodeBenchStatus(nodeID, bl.config.ChainID, bl.config.LongHistorySize, bl.config.ShortThresholdTimePeriod, bl.config.Time)
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

	if bl.config.IsBootstrapping() {
		return
	}

	var nodesToBench []ids.NodeID
	var benchedCandidates []ids.NodeID
	var unbenchedNodes []ids.NodeID

	successByNode := make(map[ids.NodeID]float64)

	benchedNodes := bl.benchedNodes.Load().(set.Set[ids.NodeID])

	for _, benchStatus := range bl.nodesToBenchStatus {
		benchedByHistory := benchStatus.history.shouldBeBenched()
		//recentlyOnlyFailed := benchStatus.latestEvents.shouldBeBenched()

		if benchedNodes.Contains(benchStatus.nodeID) {
			if !benchedByHistory { //&& !recentlyOnlyFailed {
				unbenchedNodes = append(unbenchedNodes, benchStatus.nodeID)
			}
		} else {
			if benchedByHistory { //|| recentlyOnlyFailed {
				benchedCandidates = append(benchedCandidates, benchStatus.nodeID)
				successByNode[benchStatus.nodeID] = benchStatus.history.successRate()
				fmt.Println(">>>> Success rate for", benchStatus.nodeID, "is", successByNode[benchStatus.nodeID])
			}
		}
	}

	// Sort the benched candidate nodes by their success rate
	sort.Slice(benchedCandidates, func(i, j int) bool {
		return successByNode[benchedCandidates[i]] < successByNode[benchedCandidates[j]]
	})

	benchedStake, totalStake, err := bl.computeBenchedStake()
	if err != nil {
		bl.config.Logger.Error("error calculating benched stake", zap.Error(err))
	}

	// See how much stake we get back after unbenching the unbenched candidates
	for _, unbenchedCandidate := range unbenchedNodes {
		benchedStake, err = safemath.Sub(benchedStake, bl.config.Weight(unbenchedCandidate))
		if err != nil {
			bl.config.Logger.Error("overflow calculating benched stake, unbenched stake overflows", zap.Error(err))
			return
		}
	}

	// Remove all unbenched candidates from the benched set
	for _, unbenchedNode := range unbenchedNodes {
		fmt.Println(">>>> Removing", unbenchedNode, "from the benched set")
		benchedNodes.Remove(unbenchedNode)
	}

	maxBenchedStake := uint64(float64(totalStake) * bl.config.MaxAllowedBenchedStakePercent)

	for _, benchedCandidate := range benchedCandidates {
		stakeOfNode := bl.config.Weight(benchedCandidate)
		totalBenchedStakeIfAdded, err := safemath.Add(benchedStake, stakeOfNode)
		if err != nil {
			bl.config.Logger.Error("overflow calculating future benched stake, stake overflows", zap.Error(err))
			return
		}
		if totalBenchedStakeIfAdded > maxBenchedStake {
			continue
		}
		nodesToBench = append(nodesToBench, benchedCandidate)
		benchedStake, err = safemath.Add(benchedStake, stakeOfNode)
		if err != nil {
			bl.config.Logger.Error("overflow calculating benched stake, total stake overflows", zap.Error(err))
			return
		}
		benchedNodes.Add(benchedCandidate)
		fmt.Println(">>>> Added", benchedCandidate, "to the benched set")
	}

	bl.benchedNodes.Store(benchedNodes)
	bl.config.OnBenchedOrUnbench(benchedNodes.Len(), int64(benchedStake))

	go func() {
		for _, benchedNode := range nodesToBench {
			bl.config.BenchNotifier.Benched(bl.config.ChainID, benchedNode)
		}
		for _, unBenchedNode := range unbenchedNodes {
			bl.config.BenchNotifier.Unbenched(bl.config.ChainID, unBenchedNode)
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
