package poll

import (
	"fmt"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/bag"
)

type AnalyzerFactory struct {
	Name    string
	Factory Factory
}

func (af *AnalyzerFactory) New(vdrs bag.Bag[ids.NodeID]) Poll {
	return &Analyzer{poll: af.Factory.New(vdrs).(*earlyTermNoTraversalPoll), Name: af.Name}
}

type Analyzer struct {
	Name string
	poll *earlyTermNoTraversalPoll
}

func (a *Analyzer) String() string {
	return a.poll.String()
}

func (a *Analyzer) PrefixedString(prefix string) string {
	return a.poll.PrefixedString(prefix)
}

func (a *Analyzer) Vote(vdr ids.NodeID, vote ids.ID) {
	fmt.Println(">>>", a.Name, "VOTE", vdr, vote)
	a.poll.Vote(vdr, vote)
}

func (a *Analyzer) Drop(vdr ids.NodeID) {
	fmt.Println(">>>", a.Name, "DROP", vdr)
	a.poll.Drop(vdr)
}

func (a *Analyzer) Finished() bool {
	finished, reason := a.poll.finishedAndReason()
	fmt.Println(">>>", a.Name, "FINISHED", reason)
	return finished
}

func (a *Analyzer) Result() bag.Bag[ids.ID] {
	return a.poll.Result()
}
