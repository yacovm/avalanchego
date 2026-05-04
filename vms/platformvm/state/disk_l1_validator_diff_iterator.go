// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package state

import (
	"encoding/binary"
	"fmt"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
)

// L1 validator reverse diff
//
// For each block height in which an L1 validator's set membership changed
// (added, removed, or weight changed), one entry is written per affected
// validationID with the prior on-disk membership state. The diffs allow
// reconstructing the membership at any historical height by starting from
// the current set and walking the diffs backwards.
//
// Membership is the projection (NodeID, PublicKey, Weight) — i.e. it ignores
// EndAccumulatedFee. SubnetID/NodeID/PublicKey are immutable for a given
// validationID, so in practice the only mutable membership field is Weight.
//
// Key:   subnetID (32) || ~height (8) || validationID (32)
// Value: see [marshalL1ValidatorDiff].
const (
	// l1DiffStartKeyLength = [subnetID] + [~height]
	l1DiffStartKeyLength = ids.IDLen + database.Uint64Size
	// l1DiffKeyLength = [subnetID] + [~height] + [validationID]
	l1DiffKeyLength = l1DiffStartKeyLength + ids.IDLen
	// l1DiffKeyValidationIDOffset = [subnetID] + [~height]
	l1DiffKeyValidationIDOffset = ids.IDLen + database.Uint64Size

	// uint32Size is the size in bytes of a big-endian uint32 length prefix.
	uint32Size = 4
	// l1DiffValueMinLength = [existed]
	l1DiffValueMinLength = database.BoolSize
)

var (
	errUnexpectedL1DiffKeyLength   = fmt.Errorf("expected L1 diff key length %d", l1DiffKeyLength)
	errUnexpectedL1DiffValueLength = fmt.Errorf("L1 diff value too short")
)

// l1ValidatorDiff is the prior membership state of an L1 validator at the
// height encoded in the key. If [Existed] is false, the validator did not
// exist on disk before the change (i.e. the change at this height was an
// addition; reversing it means deleting from the set).
type l1ValidatorDiff struct {
	Existed   bool
	NodeID    ids.NodeID
	PublicKey []byte // uncompressed BLS public key bytes
	Weight    uint64
}

func marshalL1ValidatorDiffKey(subnetID ids.ID, height uint64, validationID ids.ID) []byte {
	key := make([]byte, l1DiffKeyLength)
	copy(key, subnetID[:])
	packIterableHeight(key[ids.IDLen:], height)
	copy(key[l1DiffKeyValidationIDOffset:], validationID[:])
	return key
}

func unmarshalL1ValidatorDiffKey(key []byte) (ids.ID, uint64, ids.ID, error) {
	if len(key) != l1DiffKeyLength {
		return ids.Empty, 0, ids.Empty, errUnexpectedL1DiffKeyLength
	}
	var (
		subnetID     ids.ID
		validationID ids.ID
	)
	copy(subnetID[:], key)
	height := unpackIterableHeight(key[ids.IDLen:])
	copy(validationID[:], key[l1DiffKeyValidationIDOffset:])
	return subnetID, height, validationID, nil
}

// marshalL1ValidatorDiff encodes the prior membership state.
//
// If [Existed] is false, the value is a single zero byte. Otherwise it is:
//
//	[1: existed=1] [8: weight BE] [20: nodeID] [4: pkLen BE] [pkLen: publicKey].
func marshalL1ValidatorDiff(diff *l1ValidatorDiff) []byte {
	if !diff.Existed {
		return []byte{database.BoolFalse}
	}

	pkLen := uint32(len(diff.PublicKey))
	value := make([]byte, database.BoolSize+database.Uint64Size+ids.NodeIDLen+uint32Size+len(diff.PublicKey))
	value[0] = database.BoolTrue
	off := database.BoolSize
	binary.BigEndian.PutUint64(value[off:], diff.Weight)
	off += database.Uint64Size
	copy(value[off:], diff.NodeID[:])
	off += ids.NodeIDLen
	binary.BigEndian.PutUint32(value[off:], pkLen)
	off += uint32Size
	copy(value[off:], diff.PublicKey)
	return value
}

func unmarshalL1ValidatorDiff(value []byte) (*l1ValidatorDiff, error) {
	if len(value) < l1DiffValueMinLength {
		return nil, errUnexpectedL1DiffValueLength
	}
	if value[0] == database.BoolFalse {
		return &l1ValidatorDiff{Existed: false}, nil
	}

	const headerLen = database.BoolSize + database.Uint64Size + ids.NodeIDLen + uint32Size
	if len(value) < headerLen {
		return nil, errUnexpectedL1DiffValueLength
	}

	diff := &l1ValidatorDiff{Existed: true}
	off := database.BoolSize
	diff.Weight = binary.BigEndian.Uint64(value[off:])
	off += database.Uint64Size
	copy(diff.NodeID[:], value[off:off+ids.NodeIDLen])
	off += ids.NodeIDLen
	pkLen := binary.BigEndian.Uint32(value[off:])
	off += uint32Size
	if uint32(len(value)-off) != pkLen {
		return nil, errUnexpectedL1DiffValueLength
	}
	diff.PublicKey = make([]byte, pkLen)
	copy(diff.PublicKey, value[off:])
	return diff, nil
}
