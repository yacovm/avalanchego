package snowball

import (
	"crypto/rand"
	"fmt"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/bag"
	"github.com/ava-labs/avalanchego/utils/sampler"
	"github.com/stretchr/testify/require"
	"gonum.org/v1/gonum/mathext/prng"
	"math"
	"testing"
)

type network struct {
	nodes []Consensus
	U     sampler.Uniform
}

func (n network) poll(minPartitionIndex, maxPartitionIndex uint64, k int) bag.Bag[ids.ID] {
	indices, ok := n.U.Sample(k)
	if !ok {
		panic("sampling failed")
	}

	var votes bag.Bag[ids.ID]
	for _, index := range indices {
		if index < minPartitionIndex || index > maxPartitionIndex {
			continue
		}
		votes.Add(n.nodes[index].Preference())
	}
	return votes
}

func TestPartition(t *testing.T) {
	require := require.New(t)

	var block1 ids.ID
	_, err := rand.Read(block1[:])
	require.NoError(err)

	var block2 ids.ID
	_, err = rand.Read(block2[:])
	require.NoError(err)

	params := Parameters{
		K:               20,
		AlphaPreference: 15,
		AlphaConfidence: 15,
		Beta:            20,
	}

	var network network
	network.nodes = make([]Consensus, 500)
	network.U = sampler.NewDeterministicUniform(prng.NewMT19937_64())
	network.U.Initialize(uint64(len(network.nodes)))
	initialChoices := []ids.ID{block1, block2}

	half := len(network.nodes) / 2
	for i := range network.nodes {
		choice := initialChoices[0]
		if i > half {
			choice = initialChoices[1]
		}
		tree := NewTree(SnowballFactory, params, choice)
		network.nodes[i] = tree
	}

	// Simulate execution of partition I
	min := uint64(0)
	max := uint64(half)
	runNetwork(network, min, max, params)

	// Simulate execution of partition II
	min = uint64(half + 1)
	max = math.MaxUint
	runNetwork(network, min, max, params)

	for i, n := range network.nodes {
		choice := initialChoices[1]
		if i > half {
			choice = initialChoices[0]
		}
		n.Add(choice)
	}

	min = 0
	max = math.MaxUint
	runNetwork(network, min, max, params)
	runNetwork(network, min, max, params)

	var allFinalized bool
	var someFinalized bool

	allFinalized = true

	for _, n := range network.nodes {
		if n.Finalized() {
			someFinalized = true
		} else {
			allFinalized = false
		}
	}

	fmt.Println("someFinalized:", someFinalized, "allFinalized:", allFinalized)

}

func runNetwork(network network, min uint64, max uint64, params Parameters) {
	for i := 0; i < 100; i++ {
		for _, n := range network.nodes {
			votes := network.poll(min, max, params.K)
			n.RecordPoll(votes)
		}
	}
}
