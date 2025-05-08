// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"context"
	"encoding/hex"
	"github.com/ava-labs/avalanchego/utils/constants"
	"log"

	"github.com/ava-labs/avalanchego/api/info"
	"github.com/ava-labs/avalanchego/genesis"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/platformvm/warp/message"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
)

func main() {

	var conversionData []*txs.ConvertSubnetToL1Validator

	for _, uri := range []string{
		"http://localhost:9650",
		"http://localhost:9660",
		"http://localhost:9670",
		"http://localhost:9680",
		"http://localhost:9690",
	} {
		cd := getValidator(uri)
		conversionData = append(conversionData, &cd)
	}

	ctx := context.Background()
	key := genesis.EWOQKey
	kc := secp256k1fx.NewKeychain(key)

	// MakePWallet fetches the available UTXOs owned by [kc] on the P-chain that
	// [uri] is hosting and registers [subnetID].

	subnetID := ids.FromStringOrPanic("BKBZ6xXTnT86B4L5fp8rvtcmNSpvtNz8En9jG61ywV2uWyeHy")

	wallet, err := primary.MakePWallet(
		ctx,
		"http://localhost:9650",
		kc,
		primary.WalletConfig{
			SubnetIDs: []ids.ID{subnetID},
		},
	)
	if err != nil {
		log.Fatalf("failed to initialize wallet: %s\n", err)
	}

	//chainID := ids.FromStringOrPanic("ugwur9uedcTZ8di3PZjG4rLDnepzCYSE8o2YDEJPSHusCGcCP")
	chainID := constants.PlatformChainID
	addressHex := ""
	address, err := hex.DecodeString(addressHex)

	convertSubnetToL1Tx, err := wallet.IssueConvertSubnetToL1Tx(
		subnetID,
		chainID,
		address,
		conversionData,
	)
	if err != nil {
		log.Fatalf("failed to issue subnet conversion transaction: %s\n", err)
	}
	log.Printf("converted subnet %s with transactionID %s",
		subnetID,
		convertSubnetToL1Tx.ID(),
	)
}

func getValidator(uri string) txs.ConvertSubnetToL1Validator {
	ctx := context.Background()

	infoClient := info.NewClient(uri)
	nodeID, nodePoP, err := infoClient.GetNodeID(ctx)
	if err != nil {
		log.Fatalf("failed to fetch node IDs: %s\n", err)
	}

	return txs.ConvertSubnetToL1Validator{
		NodeID:                nodeID.Bytes(),
		Balance:               units.Avax,
		Signer:                *nodePoP,
		RemainingBalanceOwner: message.PChainOwner{},
		DeactivationOwner:     message.PChainOwner{},
		Weight:                units.Schmeckle,
	}
}
