// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package msm

type SimplexEpochInfo struct {
	PChainReferenceHeight     uint64                     `canoto:"uint,1"`
	EpochNumber               uint64                     `canoto:"uint,2"`
	PrevSealingBlockHash      []byte                     `canoto:"bytes,3"`
	NextPChainReferenceHeight uint64                     `canoto:"uint,4"`
	PrevVMBlockSeq            uint64                     `canoto:"uint,5"`
	BlockValidationDescriptor *BlockValidationDescriptor `canoto:"field,6"`
	NextEpochApprovals        *NextEpochApprovals        `canoto:"field,7"`
	SealingBlockSeq           uint64                     `canoto:"uint,8"`

	canotoData canotoData_SimplexEpochInfo
}

type NodeBLSMapping struct {
	nodeID []byte
	BLSKey []byte
}

type BlockValidationDescriptor struct {
	AggregatedMembership *AggregatedMembership `canoto:"field,1"`
}

type AggregatedMembership struct {
	Members []*NodeBLSMapping `canoto:"field,1"`
}

type NextEpochApprovals struct {
	NodeIDs   []byte `canoto:"bytes,1"`
	Signature []byte `canoto:"bytes,2"`
}
