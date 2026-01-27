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
	"github.com/ava-labs/avalanchego/utils/cb58"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/utils/set"

	safemath "github.com/ava-labs/avalanchego/utils/math"
)

type historyBasedBenching interface {
	markFailure(time.Time)
	markSuccess(time.Time)
	shouldBeBenched() bool
	successRate() float64
}

var bannedNodesStrings = []string{
	"BXZr17N75YvPPaLqRPdyNATT1e8oxwRnF",
	"4pmpS7zznorSbCyDRxnfFriggu8pfz2Kv",
	"EboRgvw57eqzfafbFxwF3NAPLLFaNQjin",
	"HqeE7wcqkoZwhnwv6ADxudhZvoSH76pcw",
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
	events        atomic.Uint64
	successes     atomic.Uint64
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
			unbenchThreshold: 0.5,
			avg:              math.NewSyncAverager(math.NewAverager(1, time.Minute, getTime())),
		},
	}
}

func (bns *nodeBenchStatus) isBenched() bool {
	return bns.benched.Load()
}

func (bns *nodeBenchStatus) markFailure(t time.Time) {
	bns.events.Add(1)
	bns.history.markFailure(t)
	bns.latestEvents.markFailure(t)
}

func (bns *nodeBenchStatus) markSuccess(t time.Time) {
	bns.successes.Add(1)
	bns.events.Add(1)
	bns.history.markSuccess(t)
	bns.latestEvents.markFailure(t)
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

	delinquentNodes := set.NewSet[ids.NodeID](0)

	for _, bn := range bannedNodesStrings {
		bannedNode, err := cb58.Decode(bn)
		if err != nil {
			panic(err)
		}

		delinquentNodes.Add(ids.NodeID(bannedNode))
	}

	var benchedCandidates []ids.NodeID

	eventCountByNode := make(map[ids.NodeID]uint64)

	for _, benchStatus := range bl.nodesToBenchStatus {
		if delinquentNodes.Contains(benchStatus.nodeID) {
			fmt.Println("Delinquent node", benchStatus.nodeID, "has", benchStatus.successes.Load(), "successes out of", benchStatus.events.Load(), "sucesses or failures")
			benchStatus.latestEvents.shouldBeBenched()
		}
		benchedByHistory := benchStatus.history.shouldBeBenched()
		// onlyFailed := benchStatus.latestEvents.shouldBeBenched()
		eventCountByNode[benchStatus.nodeID] = benchStatus.events.Load()

		if benchStatus.history.successRate() < 0.95 && !benchedByHistory {
			fmt.Println(">>>> Node", benchStatus.nodeID, "has", benchStatus.history.successRate(), "success rate")
		}

		if benchedByHistory { //|| onlyFailed {
			benchedCandidates = append(benchedCandidates, benchStatus.nodeID)
		}
	}

	// Sort the benched candidate nodes by their events
	sort.Slice(benchedCandidates, func(i, j int) bool {
		return eventCountByNode[benchedCandidates[i]] > eventCountByNode[benchedCandidates[j]]
	})

	// Sanity test: Ensure they are in decreasing order
	for i := 0; i < len(benchedCandidates)-1; i++ {
		if eventCountByNode[benchedCandidates[i]] < eventCountByNode[benchedCandidates[i+1]] {
			panic("benched candidates are not in decreasing order")
		}
	}

	// Pick only the top 10 heavy hitters
	// limit := min(len(benchedCandidates), 10)
	// benchedCandidates = benchedCandidates[:limit]
	futureBenchedCandidates := set.Of(benchedCandidates...)

	benchedStake, totalStake, err := bl.computeBenchedStake()
	if err != nil {
		bl.config.Logger.Error("error calculating benched stake", zap.Error(err))
	}

	previousBenchedNodes := bl.benchedNodes.Load().(set.Set[ids.NodeID])

	var unbenchedNodes []ids.NodeID
	var alreadyBenchedNodes []ids.NodeID

	// If the node was previously benched but isn't a candidate to be benched, mark it unbenched
	for node := range previousBenchedNodes {
		if !futureBenchedCandidates.Contains(node) {
			unbenchedNodes = append(unbenchedNodes, node)
			fmt.Println(">>>> Unbenching", node, "from the benched set")
			benchedStake, err = safemath.Sub(benchedStake, bl.config.Weight(node))
			if err != nil {
				bl.config.Logger.Error("overflow calculating benched stake, unbenched stake overflows", zap.Error(err))
				return
			}
		}
	}

	maxBenchedStake := uint64(float64(totalStake) * bl.config.MaxAllowedBenchedStakePercent)

	stakeRemainingToBeBenched, err := safemath.Sub(maxBenchedStake, benchedStake)
	if err != nil {
		bl.config.Logger.Error("overflow calculating max allowed stake to be benched, max allowed stake to be benched overflows", zap.Error(err))
		return
	}

	var nodesToBench []ids.NodeID

	fmt.Println("Iterating through", len(futureBenchedCandidates), "candidates to be benched")

	for node := range futureBenchedCandidates {
		// Node is already benched, skip it.
		if previousBenchedNodes.Contains(node) {
			alreadyBenchedNodes = append(alreadyBenchedNodes, node)
			continue
		}
		stakeOfNode := bl.config.Weight(node)
		if stakeOfNode > stakeRemainingToBeBenched {
			fmt.Println(">>>> Will not bench", node, "because it has too much stake")
			continue
		}
		stakeRemainingToBeBenched, err = safemath.Sub(stakeRemainingToBeBenched, stakeOfNode)
		if err != nil {
			bl.config.Logger.Error("overflow calculating stake remaining to be benched, total stake overflows", zap.Error(err))
			return
		}
		benchedStake, err = safemath.Add(benchedStake, stakeOfNode)
		if err != nil {
			bl.config.Logger.Error("overflow calculating benched stake, benched stake overflows", zap.Error(err))
			return
		}

		nodesToBench = append(nodesToBench, node)
	}

	newBenchedNodes := make([]ids.NodeID, 0, len(nodesToBench)+len(alreadyBenchedNodes))
	newBenchedNodes = append(newBenchedNodes, alreadyBenchedNodes...)
	newBenchedNodes = append(newBenchedNodes, nodesToBench...)

	prevBenchedCount := previousBenchedNodes.Len()

	bl.benchedNodes.Store(set.Of[ids.NodeID](newBenchedNodes...))
	bl.config.OnBenchedOrUnbench(len(newBenchedNodes), int64(benchedStake))

	fmt.Println(">>>>> benched node count changed from", prevBenchedCount, "to", len(newBenchedNodes))
	fmt.Println("Previously benched nodes: ", previousBenchedNodes, "New benched nodes: ", nodesToBench,
		"can bench", 100*stakeRemainingToBeBenched/totalStake, "percent of the total stake on chain", bl.config.ChainID)

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

	fmt.Println(">>>> Total", benchedNodes.Len(), "nodes are benched", bl.config.ChainID)

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
