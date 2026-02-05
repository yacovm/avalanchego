// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package metadata

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/ava-labs/simplex"

	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/utils/set"

	safemath "github.com/ava-labs/avalanchego/utils/math"
)

type verificationInput struct {
	prevMD        StateMachineMetadata
	nextMD        StateMachineMetadata
	nextTimestamp time.Time
	hasChildBlock bool
	nextBlockType blockType
	state         state
}

type verifier interface {
	Verify(in verificationInput) error
}

type validationDescriptorVerifier struct {
	getValidatorSet ValidatorSetRetriever
}

func (vd *validationDescriptorVerifier) Verify(prevMD, nextMD StateMachineMetadata, nextBlockType blockType, state state) error {
	prev, next := prevMD.SimplexEpochInfo, nextMD.SimplexEpochInfo
	switch nextBlockType {
	case blockTypeSealing:
		return vd.verifySealingBlock(prev, next)
	default:
		return vd.verifyEmptyValidationDescriptor(prev, next)
	}
}

func (vd *validationDescriptorVerifier) verifySealingBlock(_ SimplexEpochInfo, next SimplexEpochInfo) error {
	validators, err := vd.getValidatorSet(next.NextPChainReferenceHeight)
	if err != nil {
		return err
	}

	if !validators.Compare(next.BlockValidationDescriptor.AggregatedMembership.Members) {
		return fmt.Errorf("expected validator set specified at P-chain height %d does not match validator set encoded in new block", next.NextPChainReferenceHeight)
	}

	return nil
}

func (vd *validationDescriptorVerifier) verifyEmptyValidationDescriptor(_ SimplexEpochInfo, next SimplexEpochInfo) error {
	if next.BlockValidationDescriptor != nil {
		return fmt.Errorf("block validation descriptor should be nil but got %v", next.BlockValidationDescriptor)
	}
	return nil
}

type nextEpochApprovalsVerifier struct {
	sigVerifier     SignatureVerifier
	getValidatorSet ValidatorSetRetriever
	keyAggregator   KeyAggregator
}

func (nv *nextEpochApprovalsVerifier) Verify(prevMD, nextMD StateMachineMetadata, nextBlockType blockType, state state) error {
	prev, next := prevMD.SimplexEpochInfo, nextMD.SimplexEpochInfo

	switch nextBlockType {
	case blockTypeSealing:
		return nv.verifySealingBlock(prev, next, nextMD.AuxiliaryInfo)
	case blockTypeNormal:
		return nv.verifyNormal(prev, next, nextMD.AuxiliaryInfo)
	default:
		return nv.verifyEmptyNextEpochApprovals(prev, next)
	}
}

func (nv *nextEpochApprovalsVerifier) verifySealingBlock(prev SimplexEpochInfo, next SimplexEpochInfo, auxInfo *AuxiliaryInfo) error {
	if next.NextEpochApprovals == nil {
		return fmt.Errorf("next epoch approvals should not be nil for a sealing block")
	}

	validators, err := nv.getValidatorSet(next.NextPChainReferenceHeight)
	if err != nil {
		return err
	}

	err = nv.verifySignature(prev, next, auxInfo, validators)
	if err != nil {
		return err
	}

	canSeal, err := canSealBlock(validators, next)
	if err != nil {
		return err
	}

	if !canSeal {
		return fmt.Errorf("not enough approvals to seal block")
	}

	return nil
}

func (nv *nextEpochApprovalsVerifier) verifyNormal(prev SimplexEpochInfo, next SimplexEpochInfo, auxInfo *AuxiliaryInfo) error {
	if next.NextEpochApprovals == nil {
		return fmt.Errorf("next epoch approvals should not be nil for a sealing block")
	}

	if prev.PChainReferenceHeight == 0 {
		return nil
	}

	// Otherwise, prev.PChainReferenceHeight > 0, so this means we're collecting approvals

	validators, err := nv.getValidatorSet(next.NextPChainReferenceHeight)
	if err != nil {
		return err
	}

	err = nv.verifySignature(prev, next, auxInfo, validators)
	if err != nil {
		return err
	}

	if err := areNextEpochApprovalsSignersSupersetOfPrevBlock(prev, next); err != nil {
		return err
	}

	return nil
}

func areNextEpochApprovalsSignersSupersetOfPrevBlock(prev SimplexEpochInfo, next SimplexEpochInfo) error {
	// Make sure that previous signers are still there.
	prevSigners := set.BitsFromBytes(prev.NextEpochApprovals.NodeIDs)
	nextSigners := set.BitsFromBytes(next.NextEpochApprovals.NodeIDs)
	// Remove all bits in nextSigners from prevSigners
	prevSigners.Difference(nextSigners)
	// If we have some bits left, it means there was a bit in prevSigners that wasn't in nextSigners
	if prevSigners.Len() > 0 {
		return fmt.Errorf("some signers from parent block are missing from next epoch approvals of proposed block")
	}
	return nil
}

func (nv *nextEpochApprovalsVerifier) verifyEmptyNextEpochApprovals(_ SimplexEpochInfo, next SimplexEpochInfo) error {
	if next.NextEpochApprovals != nil {
		return fmt.Errorf("next epoch approvals should be nil but got %v", next.NextEpochApprovals)
	}
	return nil
}

func canSealBlock(validators NodeBLSMappings, next SimplexEpochInfo) (bool, error) {
	totalWeight, err := computeTotalWeight(validators)
	if err != nil {
		return false, fmt.Errorf("failed computing total weight: %w", err)
	}

	approvingNodes := set.BitsFromBytes(next.NextEpochApprovals.NodeIDs)

	approvingWeight, err := computeApprovingWeight(validators, approvingNodes)
	if err != nil {
		return false, err
	}

	if approvingWeight > math.MaxInt64 {
		return false, fmt.Errorf("approving weight is too large, overflows int64: %d", approvingWeight)
	}

	threshold := big.NewRat(2, 3)
	approvingRatio := big.NewRat(approvingWeight, totalWeight)

	canSeal := approvingRatio.Cmp(threshold) > 0
	return canSeal, nil
}

func computeApprovingWeight(validators NodeBLSMappings, approvingNodes set.Bits) (int64, error) {
	var approvingWeight uint64
	var err error
	validators.ForEach(func(i int, nbm NodeBLSMapping) {
		if err != nil {
			return
		}
		if !approvingNodes.Contains(i) {
			return
		}
		approvingWeight, err = safemath.Add(approvingWeight, nbm.Weight)
	})

	if err != nil {
		return 0, fmt.Errorf("failed to compute approving weights: %w", err)
	}

	if approvingWeight > math.MaxInt64 {
		return 0, fmt.Errorf("approving weight of validators is too big, overflows int64: %d", approvingWeight)
	}

	return int64(approvingWeight), nil
}

func (nv *nextEpochApprovalsVerifier) verifySignature(prev SimplexEpochInfo, next SimplexEpochInfo, auxinfo *AuxiliaryInfo, validators NodeBLSMappings) error {
	approvingNodes := set.BitsFromBytes(next.NextEpochApprovals.NodeIDs)
	publicKeys := make([][]byte, 0, len(validators))
	validators.ForEach(func(i int, nbm NodeBLSMapping) {
		if !approvingNodes.Contains(i) {
			return
		}
		publicKeys = append(publicKeys, nbm.BLSKey)
	})

	aggPK, err := nv.keyAggregator.AggregateKeys(publicKeys...)
	if err != nil {
		return fmt.Errorf("failed to aggregate public keys: %w", err)
	}

	pChainHeightBuff := pChainReferenceHeightAsBytes(prev)

	var bb bytes.Buffer
	bb.Write(pChainHeightBuff)
	if auxinfo != nil {
		bb.Write(auxinfo.Info)
	}

	if err := nv.sigVerifier.VerifySignature(next.NextEpochApprovals.Signature, bb.Bytes(), aggPK); err != nil {
		return fmt.Errorf("failed to verify signature: %w", err)
	}
	return nil
}

func pChainReferenceHeightAsBytes(prev SimplexEpochInfo) []byte {
	pChainHeight := prev.PChainReferenceHeight
	pChainHeightBuff := make([]byte, 8)
	binary.BigEndian.PutUint64(pChainHeightBuff, pChainHeight)
	return pChainHeightBuff
}

type nextPChainReferenceHeightVerifier struct {
	getValidatorSet ValidatorSetRetriever
	getPChainHeight func() uint64
}

func (n *nextPChainReferenceHeightVerifier) Verify(prevMD, nextMD StateMachineMetadata, nextBlockType blockType, state state) error {
	prev, next := prevMD.SimplexEpochInfo, nextMD.SimplexEpochInfo
	switch nextBlockType {
	case blockTypeTelock, blockTypeSealing:
		if prev.NextPChainReferenceHeight != next.NextPChainReferenceHeight {
			return fmt.Errorf("expected P-chain reference height to be %d but got %d", prev.PChainReferenceHeight, next.PChainReferenceHeight)
		}
	case blockTypeNormal:
		return n.verifyNextPChainHeightNormal(prevMD, prev, next)
	case blockTypeNewEpoch:
		if next.NextPChainReferenceHeight != 0 {
			return fmt.Errorf("expected P-chain reference height to be 0 but got %d", next.PChainReferenceHeight)
		}
	default:
		return fmt.Errorf("unknown block type: %d", nextBlockType)
	}
	return nil
}

func (n *nextPChainReferenceHeightVerifier) verifyNextPChainHeightNormal(prevMD StateMachineMetadata, prev SimplexEpochInfo, next SimplexEpochInfo) error {
	if prev.NextPChainReferenceHeight > 0 {
		if next.NextPChainReferenceHeight != prev.NextPChainReferenceHeight {
			return fmt.Errorf("expected P-chain reference height to be %d but got %d", prev.NextPChainReferenceHeight, next.NextPChainReferenceHeight)
		}
		return nil
	}
	currentValidatorSet, err := n.getValidatorSet(prevMD.SimplexEpochInfo.PChainReferenceHeight)
	if err != nil {
		return err
	}

	newValidatorSet, err := n.getValidatorSet(next.NextPChainReferenceHeight)
	if err != nil {
		return err
	}

	if currentValidatorSet.Compare(newValidatorSet) {
		return fmt.Errorf("expected validator set specified at P-chain height %d to be not the same as the validator set specified at P-chain height %d", prev.NextPChainReferenceHeight, prev.PChainReferenceHeight)
	}

	pChainHeight := n.getPChainHeight()

	if pChainHeight < next.NextPChainReferenceHeight {
		return fmt.Errorf("haven't reached P-chain height %d yet, current P-chain height is only %d", next.NextPChainReferenceHeight, pChainHeight)
	}

	return nil
}

type epochNumberVerifier struct{}

func (e *epochNumberVerifier) Verify(prevMD, nextMD StateMachineMetadata, nextBlockType blockType, state state) error {
	prev, next := prevMD.SimplexEpochInfo, nextMD.SimplexEpochInfo
	if prev.EpochNumber != next.EpochNumber {
		return fmt.Errorf("expected epoch number to be %d but got %d", prev.EpochNumber, next.EpochNumber)
	}

	switch nextBlockType {
	case blockTypeNewEpoch:
		if prev.SealingBlockSeq != next.EpochNumber {
			return fmt.Errorf("expected epoch number to be %d but got %d", prev.SealingBlockSeq, next.EpochNumber)
		}
	default:
		if prev.EpochNumber != next.EpochNumber {
			return fmt.Errorf("expected epoch number to be %d but got %d", prev.EpochNumber, next.EpochNumber)
		}
	}
	return nil
}

type sealingBlockSeqVerifier struct{}

func (s *sealingBlockSeqVerifier) Verify(prevMD, nextMD StateMachineMetadata, nextBlockType blockType, state state) error {
	prev, next := prevMD.SimplexEpochInfo, nextMD.SimplexEpochInfo
	if prev.SealingBlockSeq != next.SealingBlockSeq {
		return fmt.Errorf("expected sealing block sequence number to be %d but got %d", prev.SealingBlockSeq, next.SealingBlockSeq)
	}

	switch nextBlockType {
	case blockTypeNewEpoch, blockTypeNormal:
		if next.SealingBlockSeq != 0 {
			return fmt.Errorf("expected sealing block sequence number to be 0 but got %d", next.SealingBlockSeq)
		}
	case blockTypeTelock:
		if next.SealingBlockSeq != prev.SealingBlockSeq {
			return fmt.Errorf("expected sealing block sequence number to be %d but got %d", prev.SealingBlockSeq, next.SealingBlockSeq)
		}
	case blockTypeSealing:
		md, err := simplex.ProtocolMetadataFromBytes(prevMD.SimplexProtocolMetadata)
		if err != nil {
			return fmt.Errorf("failed parsing protocol metadata: %w", err)
		}
		if next.SealingBlockSeq != md.Seq+1 {
			return fmt.Errorf("expected sealing block sequence number to be %d but got %d", md.Seq+1, next.SealingBlockSeq)
		}
	default:
		return fmt.Errorf("unknown block type: %d", nextBlockType)
	}

	return nil
}

type pChainHeightVerifier struct{}

func (p *pChainHeightVerifier) Verify(prevMD, nextMD StateMachineMetadata, nextBlockType blockType, state state) error {
	prev, next := prevMD.SimplexEpochInfo, nextMD.SimplexEpochInfo

	switch nextBlockType {
	case blockTypeNewEpoch:
		if prev.NextPChainReferenceHeight != next.PChainReferenceHeight {
			return fmt.Errorf("expected P-chain reference height of the first block of epoch %d to be %d but got %d",
				prev.SealingBlockSeq, prev.NextPChainReferenceHeight, next.PChainReferenceHeight)
		}
	default:
		if prev.PChainReferenceHeight != next.PChainReferenceHeight {
			return fmt.Errorf("expected P-chain reference height to be %d but got %d", prev.PChainReferenceHeight, next.PChainReferenceHeight)
		}
	}

	return nil
}

type icmEpochInfoVerifier struct {
	getUpdates      func() upgrade.Config
	computeICMEpoch ICMEpochTransition
}

func (i *icmEpochInfoVerifier) Verify(in verificationInput) error {
	prevMD, nextMD := in.prevMD, in.nextMD
	expectedICMInfo := nextICMEpochInfo(prevMD, in.hasChildBlock, i.getUpdates, i.computeICMEpoch, in.nextTimestamp)
	icmInfo := nextMD.ICMEpochInfo

}
