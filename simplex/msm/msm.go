// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package metadata

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"time"

	"github.com/ava-labs/simplex"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/utils/hashing"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/set"
)

type ICMEpochInput struct {
	ParentPChainHeight uint64
	ParentTimestamp    time.Time
	ChildTimestamp     time.Time
	ParentEpoch        ICMEpoch
}

type ICMEpoch struct {
	EpochStartTime    uint64
	EpochNumber       uint64
	PChainEpochHeight uint64
}

type StateMachineBlock struct {
	InnerBlock snowman.Block
	Metadata   StateMachineMetadata
}

func (smb *StateMachineBlock) Digest() hashing.Hash256 {
	var innerBlockHash []byte

	if smb.InnerBlock != nil {
		innerBlockBytes := smb.InnerBlock.Bytes()
		innerBlockHash = hashing.ComputeHash256(innerBlockBytes)
	}

	smbpi := StateMachineBlockPreImage{
		InnerBlockHash: innerBlockHash,
		Metadata:       smb.Metadata,
	}

	return hashing.ComputeHash256Array(smbpi.MarshalCanoto())
}

type ApprovalsRetriever interface {
	RetrieveApprovals() ValidatorSetApprovals
}

type SignatureVerifier interface {
	VerifySignature(signature []byte, message []byte, publicKey []byte) error
}

type SignatureAggregator interface {
	AggregateSignatures(signatures ...[]byte) ([]byte, error)
}

type KeyAggregator interface {
	AggregateKeys(keys ...[]byte) ([]byte, error)
}

type Block interface {
	Hash() ids.ID

	Metadata() StateMachineMetadata

	Verify(ctx context.Context) error

	HasInnerBlock() bool

	VerifySnowmanBlock(ctx context.Context) error
}

type ICMEpochTransition func(upgrade.Config, ICMEpochInput) ICMEpoch

type ValidatorSetRetriever func(pChainHeight uint64) (NodeBLSMappings, error)

type BlockRetriever func(height uint64) (Block, *simplex.Finalization, error)

type BlockBuilder interface {
	BuildBlock(ctx context.Context, pChainHeight uint64) (snowman.Block, error)

	WaitForEvent(ctx context.Context) (common.Message, error)
}

type StateMachine struct {
	MaxBlockBuildingWaitTime time.Duration
	ComputeICMEpoch          ICMEpochTransition
	GetPChainHeight          func() uint64
	GetUpgrades              func() upgrade.Config
	BlockBuilder             BlockBuilder
	Logger                   logging.Logger
	GetValidatorSet          ValidatorSetRetriever
	GetBlock                 BlockRetriever
	ApprovalsRetriever       ApprovalsRetriever
	SignatureAggregator      SignatureAggregator
}

type state uint8

const (
	stateFirstSimplexBlock state = iota
	stateBuildBlockNormalOp
	stateBuildCollectingApprovals
	stateBuildBlockEpochSealed
)

type blockType uint8

const (
	blockTypeNormal blockType = iota
	blockTypeTelock
	blockTypeSealing
	blockTypeNewEpoch
)

func (sm *StateMachine) BuildBlock(ctx context.Context, parentBlock StateMachineBlock, simplexMetadata, simplexBlacklist []byte) (*StateMachineBlock, error) {
	currentState, err := sm.identifyCurrentState(parentBlock.Metadata.SimplexEpochInfo)
	if err != nil {
		return nil, err
	}

	switch currentState {
	case stateFirstSimplexBlock:
		return sm.buildBlockZeroEpoch(ctx, parentBlock, simplexMetadata, simplexBlacklist)
	case stateBuildBlockNormalOp:
		return sm.buildBlockNormalOp(ctx, parentBlock, simplexMetadata, simplexBlacklist)
	case stateBuildCollectingApprovals:
		return sm.buildBlockCollectingApprovals(ctx, parentBlock, simplexMetadata, simplexBlacklist)
	case stateBuildBlockEpochSealed:
		return sm.buildBlockEpochSealed(ctx, parentBlock, simplexMetadata, simplexBlacklist)
	default:
		return nil, fmt.Errorf("unknown state %d", currentState)
	}
}

func (sm *StateMachine) VerifyBlock(ctx context.Context, block *StateMachineBlock) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	seq := block.InnerBlock.Height()

	if seq == 0 {
		return fmt.Errorf("attempted to build a genesis block")
	}

	prevBlock, _, err := sm.GetBlock(seq - 1)
	if err != nil {
		return fmt.Errorf("failed to retrieve previous (%d) block: %w", seq, err)
	}

	if !prevBlock.HasInnerBlock() {
		return fmt.Errorf("parent block (%d) has no inner block", seq-1)
	}

	prevState, err := sm.identifyCurrentState(prevBlock.Metadata().SimplexEpochInfo)
	if err != nil {
		return fmt.Errorf("failed to identify previous state: %w", err)
	}

	switch prevState {
	case stateFirstSimplexBlock:
		return sm.verifyBlockZeroEpoch(ctx, block, prevBlock, seq-1)
	case stateBuildBlockNormalOp:
		return sm.verifyBlockNormalOp(ctx, block, prevBlock, seq-1)
	case stateBuildCollectingApprovals:
		return sm.verifyCollectingApprovals(ctx, block, prevBlock, seq-1)
		// return sm.buildBlockCollectingApprovals(ctx, parentBlock, simplexMetadata, simplexBlacklist)
	case stateBuildBlockEpochSealed:
		return sm.verifyBlockNormalOp(ctx, block, prevBlock, seq-1)
	default:
		return fmt.Errorf("cannot identify state of previous block")
	}

	return nil
}

func (sm *StateMachine) identifyCurrentState(simplexEpochInfo SimplexEpochInfo) (state, error) {
	// If this is the first ever epoch, then this is also the first ever block to be built by Simplex.
	if simplexEpochInfo.EpochNumber == 0 {
		return stateFirstSimplexBlock, nil
	}

	if simplexEpochInfo.NextPChainReferenceHeight == 0 {
		return stateBuildBlockNormalOp, nil
	}

	// Else, NextPChainReferenceHeight > 0, so we're either in stateBuildCollectingApprovals or stateBuildBlockEpochSealed
	if simplexEpochInfo.SealingBlockSeq == 0 {
		// If we don't have a sealing block sequence yet, we're still collecting approvals for the validator set change.
		return stateBuildCollectingApprovals, nil
	}

	// Otherwise, we do have a sealing block sequence, so the epoch has been sealed.
	return stateBuildBlockEpochSealed, nil
}

func (sm *StateMachine) buildBlockNormalOp(ctx context.Context, parentBlock StateMachineBlock, simplexMetadata, simplexBlacklist []byte) (*StateMachineBlock, error) {
	pChainHeight := sm.GetPChainHeight()

	currentValidatorSet, err := sm.GetValidatorSet(parentBlock.Metadata.SimplexEpochInfo.PChainReferenceHeight)
	if err != nil {
		return nil, err
	}

	newValidatorSet, err := sm.GetValidatorSet(pChainHeight)
	if err != nil {
		return nil, err
	}

	newSimplexEpochInfo := SimplexEpochInfo{
		PChainReferenceHeight: parentBlock.Metadata.SimplexEpochInfo.PChainReferenceHeight,
		EpochNumber:           parentBlock.Metadata.SimplexEpochInfo.EpochNumber,
	}

	// If the validator set has changed, it's time to move to a new epoch.
	// We do this by setting NextPChainReferenceHeight to the new P-chain height
	// and building a block without waiting indefinitely.
	if !currentValidatorSet.Compare(newValidatorSet) {
		newSimplexEpochInfo.NextPChainReferenceHeight = pChainHeight
		return sm.buildBlockImpatiently(ctx, parentBlock, simplexMetadata, simplexBlacklist, newSimplexEpochInfo, pChainHeight)
	}

	childBlock, err := sm.BlockBuilder.BuildBlock(ctx, parentBlock.Metadata.ICMEpochInfo.PChainEpochHeight)
	if err != nil {
		return nil, err
	}

	return sm.wrapBlock(parentBlock, childBlock, newSimplexEpochInfo, pChainHeight, simplexMetadata, simplexBlacklist), nil
}

func (sm *StateMachine) verifyBlockNormalOp(ctx context.Context, block *StateMachineBlock, prevBlock Block, prevBlockSeq uint64) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	simplexEpochInfo := block.Metadata.SimplexEpochInfo

	prevSimplexEpochInfo := prevBlock.Metadata().SimplexEpochInfo

	// The common case is that the previous block and the new block are in the same epoch.
	if prevSimplexEpochInfo.EpochNumber == simplexEpochInfo.EpochNumber {
		return sm.verifyMiddleBlock(ctx, block, prevBlock, prevBlockSeq)
	}

	// Else, this block is in the edges of an epoch, either at the end or at the beginning.
	// We will identify which one it is and verify accordingly.

	// If the new block comes after a sealing block, then this block is a Telock.
	// [ Sealing Block ] <-- [ New Block ]
	if prevSimplexEpochInfo.BlockValidationDescriptor != nil {
		return sm.verifyTelock(ctx, block, prevBlock, prevBlockSeq)
	}

	// Else, if the previous block has a sealing block sequence and is in the same epoch as this block,
	// then this block has to be a Telock.
	// [ Sealing Block ] <-- [ Prev block ] <-- [ New Block ]
	if simplexEpochInfo.EpochNumber == prevSimplexEpochInfo.EpochNumber && prevSimplexEpochInfo.SealingBlockSeq != 0 {
		return sm.verifyTelock(ctx, block, prevBlock, prevBlockSeq)
	}

	// This block is the first block of its epoch if the epoch number is the sealing block sequence of the previous epoch
	if simplexEpochInfo.EpochNumber == prevSimplexEpochInfo.SealingBlockSeq {
		return sm.verifyNewEpochBlock(ctx, block, prevBlock, prevBlockSeq)
	}

	return block.InnerBlock.Verify(ctx)
}

func (sm *StateMachine) buildBlockZeroEpoch(ctx context.Context, parentBlock StateMachineBlock, simplexMetadata, simplexBlacklist []byte) (*StateMachineBlock, error) {
	pChainHeight := sm.GetPChainHeight()

	newValidatorSet, err := sm.GetValidatorSet(pChainHeight)
	if err != nil {
		return nil, err
	}

	simplexEpochInfo := constructSimplexEpochInfoForZeroEpoch(pChainHeight, newValidatorSet, parentBlock.InnerBlock.Height())

	return sm.buildBlockImpatiently(ctx, parentBlock, simplexMetadata, simplexBlacklist, simplexEpochInfo, pChainHeight)
}

func (sm *StateMachine) verifySealingBlock(ctx context.Context, block *StateMachineBlock, prevBlock Block, prevBlockSeq uint64) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	newBlockValidationDescriptor := block.Metadata.SimplexEpochInfo.BlockValidationDescriptor
	prevBlockValidationDescriptor := prevBlock.Metadata().SimplexEpochInfo.BlockValidationDescriptor
}

func (sm *StateMachine) verifyTelock(ctx context.Context, block *StateMachineBlock, prevBlock Block, prevBlockSeq uint64) error {
}

func (sm *StateMachine) verifyNewEpochBlock(ctx context.Context, block *StateMachineBlock, prevBlock Block, prevBlockSeq uint64) error {
}

func (sm *StateMachine) verifyMiddleBlock(ctx context.Context, block *StateMachineBlock, prevBlock Block, prevBlockSeq uint64) error {
}

func (sm *StateMachine) verifyCollectingApprovals(ctx context.Context, block *StateMachineBlock, prevBlock Block, prevBlockSeq uint64) error {
	// The new block can either be a sealing block or we are still collecting approvals.
	prevBlockEpochInfo := prevBlock.Metadata().SimplexEpochInfo
	if prevBlockEpochInfo.BlockValidationDescriptor != nil {
		return sm.verifySealingBlock(ctx, block, prevBlock, prevBlockSeq)
	}
	return sm.verifyMiddleBlock(ctx, block, prevBlock, prevBlockSeq)
}

func (sm *StateMachine) verifyBlockZeroEpoch(ctx context.Context, block *StateMachineBlock, prevBlockMD Block, prevBlockSeq uint64) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}

	simplexEpochInfo := block.Metadata.SimplexEpochInfo

	if simplexEpochInfo.EpochNumber != 1 {
		return fmt.Errorf("invalid epoch number (%d), should be 1", simplexEpochInfo.EpochNumber)
	}

	if !prevBlockMD.HasInnerBlock() {
		return fmt.Errorf("parent block (%d) has no inner block")
	}

	err := sm.verifyPChainHeight(prevBlockMD, simplexEpochInfo)
	if err != nil {
		return err
	}

	expectedValidatorSet, err := sm.GetValidatorSet(simplexEpochInfo.PChainReferenceHeight)
	if err != nil {
		return fmt.Errorf("failed to retrieve validator set at height %d: %w", simplexEpochInfo.PChainReferenceHeight, err)
	}

	if simplexEpochInfo.BlockValidationDescriptor == nil {
		return fmt.Errorf("invalid BlockValidationDescriptor: should not be nil")
	}

	membership := simplexEpochInfo.BlockValidationDescriptor.AggregatedMembership.Members
	if !NodeBLSMappings(membership).Compare(expectedValidatorSet) {
		return fmt.Errorf("invalid BlockValidationDescriptor: should match validator set at P-chain height %d", simplexEpochInfo.PChainReferenceHeight)
	}

	// If we have compared all fields so far, the rest of the fields we compare by constructing an explicit expected SimplexEpochInfo
	expectedSimplexEpochInfo := constructSimplexEpochInfoForZeroEpoch(simplexEpochInfo.PChainReferenceHeight, expectedValidatorSet, prevBlockSeq)

	if expectedSimplexEpochInfo != simplexEpochInfo {
		return fmt.Errorf("invalid SimplexEpochInfo: expected %v, got %v", expectedSimplexEpochInfo, simplexEpochInfo)
	}

	if block.InnerBlock == nil {
		return nil
	}

	return block.InnerBlock.Verify(ctx)
}

func (sm *StateMachine) verifyPChainHeight(prevBlockMD Block, simplexEpochInfo SimplexEpochInfo) error {
	prevPChainHeight := prevBlockMD.Metadata().PChainHeight
	currentPChainHeight := sm.GetPChainHeight()

	if simplexEpochInfo.PChainReferenceHeight < prevPChainHeight {
		return fmt.Errorf("invalid P-chain reference height (%d), should be at least parent block's P-chain height %d",
			simplexEpochInfo.PChainReferenceHeight, prevPChainHeight)
	}

	if simplexEpochInfo.PChainReferenceHeight > currentPChainHeight {
		return fmt.Errorf("invalid P-chain reference height (%d) is too big, expected to be ≤ %d",
			simplexEpochInfo.PChainReferenceHeight, currentPChainHeight)
	}
	return nil
}

func constructSimplexEpochInfoForZeroEpoch(pChainHeight uint64, newValidatorSet NodeBLSMappings, PrevVMBlockSeq uint64) SimplexEpochInfo {
	newSimplexEpochInfo := SimplexEpochInfo{
		PChainReferenceHeight: pChainHeight,
		EpochNumber:           1,
		BlockValidationDescriptor: &BlockValidationDescriptor{
			AggregatedMembership: AggregatedMembership{
				Members: newValidatorSet,
			},
		},
		NextEpochApprovals:        nil, // We don't need to collect approvals to seal the zero epoch.
		PrevVMBlockSeq:            PrevVMBlockSeq,
		SealingBlockSeq:           0,          // We don't have a sealing block in the zero epoch.
		PrevSealingBlockHash:      [32]byte{}, // The zero epoch has no previous sealing block.
		NextPChainReferenceHeight: 0,
	}
	return newSimplexEpochInfo
}

func (sm *StateMachine) buildBlockCollectingApprovals(ctx context.Context, parentBlock StateMachineBlock, simplexMetadata, simplexBlacklist []byte) (*StateMachineBlock, error) {
	newSimplexEpochInfo := SimplexEpochInfo{
		PChainReferenceHeight:     parentBlock.Metadata.SimplexEpochInfo.PChainReferenceHeight,
		EpochNumber:               parentBlock.Metadata.SimplexEpochInfo.EpochNumber,
		NextPChainReferenceHeight: parentBlock.Metadata.SimplexEpochInfo.NextPChainReferenceHeight,
	}

	validators, err := sm.GetValidatorSet(parentBlock.Metadata.SimplexEpochInfo.NextPChainReferenceHeight)
	if err != nil {
		return nil, err
	}
	approvalsFromPeers := sm.ApprovalsRetriever.RetrieveApprovals()
	auxInfo := parentBlock.Metadata.AuxiliaryInfo
	nextPChainHeight := parentBlock.Metadata.SimplexEpochInfo.NextPChainReferenceHeight
	prevNextEpochApprovals := parentBlock.Metadata.SimplexEpochInfo.NextEpochApprovals
	newApprovals, err := computeNewApprovals(prevNextEpochApprovals, auxInfo, approvalsFromPeers, nextPChainHeight, sm.SignatureAggregator, validators)
	if err != nil {
		return nil, err
	}

	if !newApprovals.canSeal {
		pChainHeight := parentBlock.Metadata.PChainHeight
		return sm.buildBlockImpatiently(ctx, parentBlock, simplexMetadata, simplexBlacklist, newSimplexEpochInfo, pChainHeight)
	}

	// Else, we create the sealing block.
	return sm.createSealingBlock(ctx, parentBlock, simplexMetadata, simplexBlacklist, newSimplexEpochInfo, newApprovals, err, nextPChainHeight)
}

func (sm *StateMachine) buildBlockImpatiently(ctx context.Context, parentBlock StateMachineBlock, simplexMetadata []byte, simplexBlacklist []byte, simplexEpochInfo SimplexEpochInfo, pChainHeight uint64) (*StateMachineBlock, error) {
	impatientContext, cancel := context.WithTimeout(ctx, sm.MaxBlockBuildingWaitTime)
	defer cancel()

	ctx = impatientContext

	childBlock, err := sm.BlockBuilder.BuildBlock(ctx, parentBlock.Metadata.ICMEpochInfo.PChainEpochHeight)
	if err != nil && ctx.Err() == nil {
		// If we got an error building the block, and we didn't time out, return the error.
		// We failed to build the block.
		return nil, err
	}
	// Else, either err == nil, and we've built the block,
	// or err != nil but ctx.Err() != nil and we have waited MaxBlockBuildingWaitTime,
	// so we need to build a block regardless of whether the inner VM wants to build a block.
	return sm.wrapBlock(parentBlock, childBlock, simplexEpochInfo, pChainHeight, simplexMetadata, simplexBlacklist), nil
}

func (sm *StateMachine) createSealingBlock(ctx context.Context, parentBlock StateMachineBlock, simplexMetadata []byte, simplexBlacklist []byte, simplexEpochInfo SimplexEpochInfo, newApprovals *approvals, err error, pChainHeight uint64) (*StateMachineBlock, error) {
	// Update the approvals and signature in the simplex epoch info for the next block
	simplexEpochInfo.NextEpochApprovals.NodeIDs = newApprovals.nodeIDs
	simplexEpochInfo.NextEpochApprovals.Signature = newApprovals.signature

	// If this is the sealing block, set the sealing block sequence.
	md, err := simplex.ProtocolMetadataFromBytes(parentBlock.Metadata.SimplexProtocolMetadata)
	if err != nil {
		return nil, err
	}
	simplexEpochInfo.SealingBlockSeq = md.Seq + 1
	validators, err := sm.GetValidatorSet(simplexEpochInfo.NextPChainReferenceHeight)
	if err != nil {
		return nil, err
	}
	simplexEpochInfo.BlockValidationDescriptor.AggregatedMembership.Members = validators

	// If this is not the first epoch, and this is the sealing block, we set the hash of the previous sealing block.
	if newApprovals.canSeal && simplexEpochInfo.EpochNumber > 0 {
		prevSealingBlock, _, err := sm.GetBlock(simplexEpochInfo.EpochNumber)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve previous sealing block at epoch %d: %w", simplexEpochInfo.EpochNumber-1, err)
		}
		simplexEpochInfo.PrevSealingBlockHash = prevSealingBlock.Hash()
	}

	return sm.buildBlockImpatiently(ctx, parentBlock, simplexMetadata, simplexBlacklist, simplexEpochInfo, pChainHeight)
}

func computeNewApprovals(
	nextEpochApprovals *NextEpochApprovals,
	auxInfo *AuxiliaryInfo,
	newApprovals ValidatorSetApprovals,
	pChainHeight uint64,
	aggregator SignatureAggregator,
	validators NodeBLSMappings,
) (*approvals, error) {
	nodeID2ValidatorIndex := make(map[ids.NodeID]int)
	validators.ForEach(func(i int, nbm NodeBLSMapping) {
		nodeID2ValidatorIndex[nbm.NodeID] = i
	})

	var candidateAuxInfoDigest [32]byte
	if auxInfo != nil {
		candidateAuxInfoDigest = sha256.Sum256(auxInfo.Info)
	}

	newApprovals = newApprovals.Filter(func(i int, approval ValidatorSetApproval) bool {
		// Pick only approvals that agree with our candidate auxiliary info digest and P-Chain height
		return approval.PChainHeight == pChainHeight && approval.AuxInfoSeqDigest == candidateAuxInfoDigest
	})

	if nextEpochApprovals == nil {
		nextEpochApprovals = &NextEpochApprovals{}
	}
	existingApprovingNodes := set.BitsFromBytes(nextEpochApprovals.NodeIDs)

	newApprovals = newApprovals.Filter(func(i int, approval ValidatorSetApproval) bool {
		approvingNodeIndexOfNewApprover := nodeID2ValidatorIndex[approval.NodeID]
		// Only pick approvals from nodes that haven't already approved
		return !existingApprovingNodes.Contains(approvingNodeIndexOfNewApprover)
	})

	newApprovingNodes := existingApprovingNodes

	// Prepare the new signatures from the new approvals that haven't approved yet and that agree with our candidate auxiliary info digest and P-Chain height.
	newSignatures := make([][]byte, 0, len(newApprovals)+1)

	newApprovals.ForEach(func(i int, approval ValidatorSetApproval) {
		approvingNodeIndexOfNewApprover := nodeID2ValidatorIndex[approval.NodeID]
		// Turn on the bit for the new approver
		newApprovingNodes.Add(approvingNodeIndexOfNewApprover)
		newSignatures = append(newSignatures, approval.Signature)
	})

	// Add the existing signature into the list of signatures to aggregate
	existingSignature := nextEpochApprovals.Signature
	newSignatures = append(newSignatures, existingSignature)

	aggregatedSignature, err := aggregator.AggregateSignatures(newSignatures...)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate signatures: %w", err)
	}

	approvingWeight := validators.Sum(func(i int, nbm NodeBLSMapping) bool {
		return newApprovingNodes.Contains(i)
	})

	if validators.TotalWeight() == 0 {
		return nil, fmt.Errorf("invalid simplex epoch info: total validator weight is 0")
	}

	threshold := big.NewRat(2, 3)
	approvingRatio := big.NewRat(int64(approvingWeight), int64(validators.TotalWeight()))

	canSeal := approvingRatio.Cmp(threshold) > 0

	return &approvals{
		canSeal:   canSeal,
		signature: aggregatedSignature,
		nodeIDs:   newApprovingNodes.Bytes(),
	}, nil
}

func (sm *StateMachine) buildBlockEpochSealed(ctx context.Context, parentBlock StateMachineBlock, simplexMetadata, simplexBlacklist []byte) (*StateMachineBlock, error) {
	// We check if the sealing block has already been finalized.
	// If not, we build a Telock block.

	newSimplexEpochInfo := SimplexEpochInfo{
		PChainReferenceHeight:     parentBlock.Metadata.SimplexEpochInfo.PChainReferenceHeight,
		EpochNumber:               parentBlock.Metadata.SimplexEpochInfo.EpochNumber,
		NextPChainReferenceHeight: parentBlock.Metadata.SimplexEpochInfo.NextPChainReferenceHeight,
		SealingBlockSeq:           parentBlock.Metadata.SimplexEpochInfo.SealingBlockSeq,
	}

	// First, we find the sequence of the sealing block.
	seq := parentBlock.Metadata.SimplexEpochInfo.SealingBlockSeq

	// Do a sanity check just in case, make sure it's defined
	if seq == 0 {
		return nil, fmt.Errorf("cannot build epoch sealed block: sealing block sequence is 0 or undefined")
	}

	_, finalization, err := sm.GetBlock(seq)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve sealing block at sequence %d: %w", seq, err)
	}

	isSealingBlockFinalized := finalization != nil

	if !isSealingBlockFinalized {
		pChainHeight := parentBlock.Metadata.PChainHeight
		return sm.wrapBlock(parentBlock, nil, newSimplexEpochInfo, pChainHeight, simplexMetadata, simplexBlacklist), nil
	}

	// Else, we build a block for the new epoch.
	newSimplexEpochInfo = SimplexEpochInfo{
		// P-chain reference height is previous block's NextPChainReferenceHeight.
		PChainReferenceHeight: parentBlock.Metadata.SimplexEpochInfo.NextPChainReferenceHeight,
		// The epoch number is the sequence of the sealing block.
		EpochNumber: parentBlock.Metadata.SimplexEpochInfo.SealingBlockSeq,
	}

	childBlock, err := sm.BlockBuilder.BuildBlock(ctx, parentBlock.Metadata.ICMEpochInfo.PChainEpochHeight)
	if err != nil {
		return nil, err
	}

	return sm.wrapBlock(parentBlock, childBlock, newSimplexEpochInfo, parentBlock.Metadata.PChainHeight, simplexMetadata, simplexBlacklist), nil
}

func (sm *StateMachine) computeICMEpoch(parentMetadata StateMachineMetadata, parentTimestamp, childTimestamp time.Time) ICMEpoch {
	upgrades := sm.GetUpgrades()

	icmEpoch := sm.ComputeICMEpoch(upgrades, ICMEpochInput{
		ParentPChainHeight: parentMetadata.PChainHeight,
		ParentTimestamp:    parentTimestamp,
		ChildTimestamp:     childTimestamp,
		ParentEpoch: ICMEpoch{
			EpochStartTime:    parentMetadata.ICMEpochInfo.EpochStartTime,
			EpochNumber:       parentMetadata.ICMEpochInfo.EpochNumber,
			PChainEpochHeight: parentMetadata.ICMEpochInfo.PChainEpochHeight,
		},
	})
	return icmEpoch
}

func (sm *StateMachine) wrapBlock(parentBlock StateMachineBlock, childBlock snowman.Block, newSimplexEpochInfo SimplexEpochInfo, pChainHeight uint64, simplexMetadata, simplexBlacklist []byte) *StateMachineBlock {
	icmEpochInfo := parentBlock.Metadata.ICMEpochInfo
	timestamp := parentBlock.Metadata.Timestamp

	if childBlock != nil {
		parentTimestamp := time.Unix(int64(parentBlock.Metadata.Timestamp), 0)
		newTimestamp := childBlock.Timestamp()
		timestamp = uint64(newTimestamp.Unix())
		icmEpoch := sm.computeICMEpoch(parentBlock.Metadata, parentTimestamp, newTimestamp)
		icmEpochInfo = ICMEpochInfo{
			EpochStartTime:    icmEpoch.EpochStartTime,
			EpochNumber:       icmEpoch.EpochNumber,
			PChainEpochHeight: icmEpoch.PChainEpochHeight,
		}
	}

	if parentBlock.InnerBlock == nil {
		newSimplexEpochInfo.PrevVMBlockSeq = parentBlock.Metadata.SimplexEpochInfo.PrevVMBlockSeq
	} else {
		newSimplexEpochInfo.PrevVMBlockSeq = parentBlock.InnerBlock.Height()
	}

	return &StateMachineBlock{
		InnerBlock: childBlock,
		Metadata: StateMachineMetadata{
			Timestamp:               timestamp,
			SimplexProtocolMetadata: simplexMetadata,
			SimplexBlacklist:        simplexBlacklist,
			SimplexEpochInfo:        newSimplexEpochInfo,
			PChainHeight:            pChainHeight,
			ICMEpochInfo:            icmEpochInfo,
		},
	}
}

type approvals struct {
	canSeal   bool
	nodeIDs   []byte
	signature []byte
}
