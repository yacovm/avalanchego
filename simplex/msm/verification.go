// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package metadata

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/big"

	"github.com/ava-labs/avalanchego/utils/set"
)

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
		return nv.verifyNormal(prev, next)
	default:
		return nv.verifyEmptyNextEpochApprovals(prev, next)
	}
}

func (nv *nextEpochApprovalsVerifier) verifySealingBlock(prev SimplexEpochInfo, next SimplexEpochInfo, auxinfo *AuxiliaryInfo, nextAuxInfo *AuxiliaryInfo) error {
	if next.NextEpochApprovals == nil {
		return fmt.Errorf("next epoch approvals should not be nil for a sealing block")
	}

	validators, err := nv.getValidatorSet(next.NextPChainReferenceHeight)
	if err != nil {
		return err
	}

	err = nv.verifySignature(prev, next, auxinfo, &validators)
	if err != nil {
		return err
	}

	if validators.TotalWeight() == 0 {
		return fmt.Errorf("invalid simplex epoch info: total validator weight is 0")
	}

	threshold := big.NewRat(2, 3)
	approvingRatio := big.NewRat(int64(approvingWeight), int64(validators.TotalWeight()))

	canSeal := approvingRatio.Cmp(threshold) > 0

	if !canSeal {
		return fmt.Errorf("not enough approvals to seal block")
	}

	return nil
}

func (nv *nextEpochApprovalsVerifier) verifySignature(prev SimplexEpochInfo, next SimplexEpochInfo, auxinfo *AuxiliaryInfo, validators *NodeBLSMappings) error {
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

	pChainHeight := prev.PChainReferenceHeight
	pChainHeightBuff := make([]byte, 8)
	binary.BigEndian.PutUint64(pChainHeightBuff, pChainHeight)
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

func (nv *nextEpochApprovalsVerifier) verifyNormal(prev SimplexEpochInfo, next SimplexEpochInfo) error {
}

func (nv *nextEpochApprovalsVerifier) verifyEmptyNextEpochApprovals(_ SimplexEpochInfo, next SimplexEpochInfo) error {
	if next.NextEpochApprovals != nil {
		return fmt.Errorf("next epoch approvals should be nil but got %v", next.NextEpochApprovals)
	}
	return nil
}
