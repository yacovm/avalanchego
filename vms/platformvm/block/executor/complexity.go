// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"errors"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/math"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/fx"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

var ErrUnsupportedTx = errors.New("unsupported transaction type")

var (
	_ txs.Visitor = (*complexityVisitor)(nil)

	errUnsupportedOutput = errors.New("unsupported output type")
	errUnsupportedInput  = errors.New("unsupported input type")
	errUnsupportedOwner  = errors.New("unsupported owner type")
	errUnsupportedAuth   = errors.New("unsupported auth type")
	errUnsupportedSigner = errors.New("unsupported signer type")
)

type Operation struct {
	BLS   int
	ECDSA int
	Read  int
	Write int
}

func (o Operation) Add(o2 *Operation) Operation {
	return Operation{
		BLS:   o.BLS + o2.BLS,
		ECDSA: o.ECDSA + o2.ECDSA,
		Read:  o.Read + o2.Read,
		Write: o.Write + o2.Write,
	}
}

func TxComplexity(txs []*txs.Tx) (Operation, error) {
	var (
		c          complexityVisitor
		complexity Operation
	)
	for _, tx := range txs {
		c = complexityVisitor{}
		err := tx.Unsigned.Visit(&c)
		if err != nil {
			return Operation{}, err
		}
		complexity = complexity.Add(&c.output)
	}
	return complexity, nil
}

// OutputComplexity returns the complexity outputs add to a transaction.
func OutputComplexity(outs ...*avax.TransferableOutput) (Operation, error) {
	var complexity Operation
	for _, out := range outs {
		outputComplexity, err := outputComplexity(out)
		if err != nil {
			return Operation{}, err
		}

		complexity = complexity.Add(&outputComplexity)
	}
	return complexity, nil
}

func outputComplexity(*avax.TransferableOutput) (Operation, error) {
	complexity := Operation{
		Write: 1,
	}
	return complexity, nil
}

// InputComplexity returns the complexity inputs add to a transaction.
// It includes the complexity that the corresponding credentials will add.
func InputComplexity(ins ...*avax.TransferableInput) (Operation, error) {
	var complexity Operation
	for _, in := range ins {
		inputComplexity, err := inputComplexity(in)
		if err != nil {
			return Operation{}, err
		}

		complexity = complexity.Add(&inputComplexity)
	}
	return complexity, nil
}

func inputComplexity(in *avax.TransferableInput) (Operation, error) {
	complexity := Operation{
		Read:  1,
		Write: 1,
		ECDSA: 1,
	}

	return complexity, nil
}

// ConvertSubnetValidatorComplexity returns the complexity the validators add to
// a transaction.
func ConvertSubnetValidatorComplexity(sovs ...*txs.ConvertSubnetValidator) (Operation, error) {
	var complexity Operation
	for _, sov := range sovs {
		sovComplexity, err := convertSubnetValidatorComplexity(sov)
		if err != nil {
			return Operation{}, err
		}

		complexity = complexity.Add(&sovComplexity)
	}
	return complexity, nil
}

func convertSubnetValidatorComplexity(sov *txs.ConvertSubnetValidator) (Operation, error) {
	complexity := Operation{
		Write: 1,
	}

	signerComplexity, err := SignerComplexity(&sov.Signer)
	if err != nil {
		return Operation{}, err
	}

	return signerComplexity.Add(&complexity), nil
}

// OwnerComplexity returns the complexity an owner adds to a transaction.
// It does not include the typeID of the owner.
func OwnerComplexity(ownerIntf fx.Owner) (Operation, error) {
	owner, ok := ownerIntf.(*secp256k1fx.OutputOwners)
	if !ok {
		return Operation{}, errUnsupportedOwner
	}

	numAddresses := uint64(len(owner.Addrs))

	return Operation{
		ECDSA: int(numAddresses),
	}, nil
}

func AuthComplexity(authIntf verify.Verifiable) (Operation, error) {
	auth, ok := authIntf.(*secp256k1fx.Input)
	if !ok {
		return Operation{}, errUnsupportedAuth
	}

	numSignatures := uint64(len(auth.SigIndices))

	return Operation{ECDSA: int(numSignatures)}, nil
}

// SignerComplexity returns the complexity a signer adds to a transaction.
// It does not include the typeID of the signer.
func SignerComplexity(s signer.Signer) (Operation, error) {
	switch s.(type) {
	case *signer.Empty:
		return Operation{}, nil
	case *signer.ProofOfPossession:
		return Operation{
			BLS: 1,
		}, nil
	default:
		return Operation{}, errUnsupportedSigner
	}
}

// WarpComplexity returns the complexity a warp message adds to a transaction.
func WarpComplexity(message []byte) (Operation, error) {
	return Operation{
		BLS: 1,
	}, nil
}

type complexityVisitor struct {
	output Operation
}

func (*complexityVisitor) AddDelegatorTx(*txs.AddDelegatorTx) error {
	return ErrUnsupportedTx
}

func (*complexityVisitor) AddValidatorTx(*txs.AddValidatorTx) error {
	return ErrUnsupportedTx
}

func (*complexityVisitor) AdvanceTimeTx(*txs.AdvanceTimeTx) error {
	return ErrUnsupportedTx
}

func (*complexityVisitor) RewardValidatorTx(*txs.RewardValidatorTx) error {
	return ErrUnsupportedTx
}

func (*complexityVisitor) TransformSubnetTx(*txs.TransformSubnetTx) error {
	return ErrUnsupportedTx
}

func (c *complexityVisitor) AddPermissionlessValidatorTx(tx *txs.AddPermissionlessValidatorTx) error {
	// TODO: Should we include additional complexity for subnets?
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	signerComplexity, err := SignerComplexity(tx.Signer)
	if err != nil {
		return err
	}
	outputsComplexity, err := OutputComplexity(tx.StakeOuts...)
	if err != nil {
		return err
	}
	validatorOwnerComplexity, err := OwnerComplexity(tx.ValidatorRewardsOwner)
	if err != nil {
		return err
	}
	delegatorOwnerComplexity, err := OwnerComplexity(tx.DelegatorRewardsOwner)
	if err != nil {
		return err
	}
	c.output = Operation{Read: 1, Write: 1}.Add(
		&baseTxComplexity).Add(
		&signerComplexity).Add(
		&outputsComplexity).Add(
		&validatorOwnerComplexity).Add(
		&delegatorOwnerComplexity)
	return err
}

func (c *complexityVisitor) AddPermissionlessDelegatorTx(tx *txs.AddPermissionlessDelegatorTx) error {
	// TODO: Should we include additional complexity for subnets?
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	ownerComplexity, err := OwnerComplexity(tx.DelegationRewardsOwner)
	if err != nil {
		return err
	}
	outputsComplexity, err := OutputComplexity(tx.StakeOuts...)
	if err != nil {
		return err
	}
	c.output = Operation{Read: 1, Write: 1}.Add(
		&baseTxComplexity).Add(
		&ownerComplexity).Add(
		&outputsComplexity)
	return nil
}

func (c *complexityVisitor) AddSubnetValidatorTx(tx *txs.AddSubnetValidatorTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	authComplexity, err := AuthComplexity(tx.SubnetAuth)
	if err != nil {
		return err
	}
	c.output = Operation{Read: 2, Write: 1}.Add(
		&baseTxComplexity).Add(&authComplexity)
	return nil
}

func (c *complexityVisitor) BaseTx(tx *txs.BaseTx) error {
	baseTxComplexity, err := baseTxComplexity(tx)
	if err != nil {
		return err
	}
	c.output = baseTxComplexity
	return nil
}

func (c *complexityVisitor) CreateChainTx(tx *txs.CreateChainTx) error {
	bandwidth, err := math.Mul(uint64(len(tx.FxIDs)), ids.IDLen)
	if err != nil {
		return err
	}
	bandwidth, err = math.Add(bandwidth, uint64(len(tx.ChainName)))
	if err != nil {
		return err
	}
	bandwidth, err = math.Add(bandwidth, uint64(len(tx.GenesisData)))
	if err != nil {
		return err
	}

	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	authComplexity, err := AuthComplexity(tx.SubnetAuth)
	if err != nil {
		return err
	}
	c.output = Operation{Read: 1, Write: 1}.Add(&baseTxComplexity).Add(&authComplexity)
	return nil
}

func (c *complexityVisitor) CreateSubnetTx(tx *txs.CreateSubnetTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	ownerComplexity, err := OwnerComplexity(tx.Owner)
	if err != nil {
		return err
	}
	c.output = Operation{Write: 1}.Add(&baseTxComplexity).Add(&ownerComplexity)
	return nil
}

func (c *complexityVisitor) ExportTx(tx *txs.ExportTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	outputsComplexity, err := OutputComplexity(tx.ExportedOutputs...)
	if err != nil {
		return err
	}

	c.output = baseTxComplexity.Add(&outputsComplexity)
	return nil
}

func (c *complexityVisitor) ImportTx(tx *txs.ImportTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	// TODO: Should imported inputs be more complex?
	inputsComplexity, err := InputComplexity(tx.ImportedInputs...)
	if err != nil {
		return err
	}

	c.output = baseTxComplexity.Add(&inputsComplexity)
	return nil
}

func (c *complexityVisitor) RemoveSubnetValidatorTx(tx *txs.RemoveSubnetValidatorTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	authComplexity, err := AuthComplexity(tx.SubnetAuth)
	if err != nil {
		return err
	}
	c.output = Operation{Read: 2, Write: 1}.Add(
		&baseTxComplexity).Add(
		&authComplexity)

	return nil
}

func (c *complexityVisitor) TransferSubnetOwnershipTx(tx *txs.TransferSubnetOwnershipTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	authComplexity, err := AuthComplexity(tx.SubnetAuth)
	if err != nil {
		return err
	}
	ownerComplexity, err := OwnerComplexity(tx.Owner)
	if err != nil {
		return err
	}
	c.output = Operation{Read: 1, Write: 1}.Add(
		&baseTxComplexity).Add(
		&authComplexity).Add(
		&ownerComplexity)

	return nil
}

func (c *complexityVisitor) ConvertSubnetTx(tx *txs.ConvertSubnetTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	validatorComplexity, err := ConvertSubnetValidatorComplexity(tx.Validators...)
	if err != nil {
		return err
	}
	authComplexity, err := AuthComplexity(tx.SubnetAuth)
	if err != nil {
		return err
	}
	c.output = Operation{Read: 2, Write: 2}.Add(
		&baseTxComplexity).Add(
		&validatorComplexity).Add(
		&authComplexity)

	return nil
}

func (c *complexityVisitor) RegisterSubnetValidatorTx(tx *txs.RegisterSubnetValidatorTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	warpComplexity, err := WarpComplexity(tx.Message)
	if err != nil {
		return err
	}
	c.output = (Operation{BLS: 1}).Add(
		&baseTxComplexity).Add(&warpComplexity)
	return nil
}

func (c *complexityVisitor) SetSubnetValidatorWeightTx(tx *txs.SetSubnetValidatorWeightTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	warpComplexity, err := WarpComplexity(tx.Message)
	if err != nil {
		return err
	}

	c.output = baseTxComplexity.Add(&warpComplexity)
	return nil

	return err
}

func (c *complexityVisitor) IncreaseBalanceTx(tx *txs.IncreaseBalanceTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	c.output = baseTxComplexity
	return err
}

func (c *complexityVisitor) DisableSubnetValidatorTx(tx *txs.DisableSubnetValidatorTx) error {
	baseTxComplexity, err := baseTxComplexity(&tx.BaseTx)
	if err != nil {
		return err
	}
	authComplexity, err := AuthComplexity(tx.DisableAuth)
	if err != nil {
		return err
	}
	c.output = baseTxComplexity.Add(&authComplexity)
	return nil
}

func baseTxComplexity(tx *txs.BaseTx) (Operation, error) {
	outputsComplexity, err := OutputComplexity(tx.Outs...)
	if err != nil {
		return Operation{}, err
	}
	inputsComplexity, err := InputComplexity(tx.Ins...)
	if err != nil {
		return Operation{}, err
	}
	complexity := outputsComplexity.Add(&inputsComplexity)

	return complexity, nil
}
