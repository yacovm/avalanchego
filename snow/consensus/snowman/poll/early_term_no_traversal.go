// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package poll

import (
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/bag"
)

var (
	errPollDurationVectorMetrics = errors.New("failed to register poll_duration vector metrics")
	errPollCountVectorMetrics    = errors.New("failed to register poll_count vector metrics")

	terminationReason    = "reason"
	exhaustedReason      = "exhausted"
	earlyFailReason      = "early_fail"
	earlyAlphaPrefReason = "early_alpha_pref"
	earlyAlphaConfReason = "early_alpha_conf"

	exhaustedLabel = prometheus.Labels{
		terminationReason: exhaustedReason,
	}
	earlyFailLabel = prometheus.Labels{
		terminationReason: earlyFailReason,
	}
	earlyAlphaPrefLabel = prometheus.Labels{
		terminationReason: earlyAlphaPrefReason,
	}
	earlyAlphaConfLabel = prometheus.Labels{
		terminationReason: earlyAlphaConfReason,
	}
)

type earlyTermNoTraversalMetrics struct {
	durExhaustedPolls      prometheus.Gauge
	durEarlyFailPolls      prometheus.Gauge
	durEarlyAlphaPrefPolls prometheus.Gauge
	durEarlyAlphaConfPolls prometheus.Gauge

	countExhaustedPolls      prometheus.Counter
	countEarlyFailPolls      prometheus.Counter
	countEarlyAlphaPrefPolls prometheus.Counter
	countEarlyAlphaConfPolls prometheus.Counter
}

func newEarlyTermNoTraversalMetrics(reg prometheus.Registerer) (*earlyTermNoTraversalMetrics, error) {
	pollCountVec := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "poll_count",
		Help: "Total # of terminated polls by reason",
	}, []string{terminationReason})
	if err := reg.Register(pollCountVec); err != nil {
		return nil, fmt.Errorf("%w: %w", errPollCountVectorMetrics, err)
	}
	durPollsVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "poll_duration",
		Help: "time (in ns) polls took to complete by reason",
	}, []string{terminationReason})
	if err := reg.Register(durPollsVec); err != nil {
		return nil, fmt.Errorf("%w: %w", errPollDurationVectorMetrics, err)
	}

	return &earlyTermNoTraversalMetrics{
		durExhaustedPolls:        durPollsVec.With(exhaustedLabel),
		durEarlyFailPolls:        durPollsVec.With(earlyFailLabel),
		durEarlyAlphaPrefPolls:   durPollsVec.With(earlyAlphaPrefLabel),
		durEarlyAlphaConfPolls:   durPollsVec.With(earlyAlphaConfLabel),
		countExhaustedPolls:      pollCountVec.With(exhaustedLabel),
		countEarlyFailPolls:      pollCountVec.With(earlyFailLabel),
		countEarlyAlphaPrefPolls: pollCountVec.With(earlyAlphaPrefLabel),
		countEarlyAlphaConfPolls: pollCountVec.With(earlyAlphaConfLabel),
	}, nil
}

func (m *earlyTermNoTraversalMetrics) observeExhausted(duration time.Duration) {
	m.durExhaustedPolls.Add(float64(duration.Nanoseconds()))
	m.countExhaustedPolls.Inc()
}

func (m *earlyTermNoTraversalMetrics) observeEarlyFail(duration time.Duration) {
	m.durEarlyFailPolls.Add(float64(duration.Nanoseconds()))
	m.countEarlyFailPolls.Inc()
}

func (m *earlyTermNoTraversalMetrics) observeEarlyAlphaPref(duration time.Duration) {
	m.durEarlyAlphaPrefPolls.Add(float64(duration.Nanoseconds()))
	m.countEarlyAlphaPrefPolls.Inc()
}

func (m *earlyTermNoTraversalMetrics) observeEarlyAlphaConf(duration time.Duration) {
	m.durEarlyAlphaConfPolls.Add(float64(duration.Nanoseconds()))
	m.countEarlyAlphaConfPolls.Inc()
}

type earlyTermNoTraversalFactory struct {
	alphaPreference  int
	alphaConfidence  int
	alphaConfidences []int
	metrics          *earlyTermNoTraversalMetrics
}

// NewEarlyTermNoTraversalFactory returns a factory that returns polls with
// early termination, without doing DAG traversals
func NewEarlyTermNoTraversalFactory(
	alphaPreference int,
	alphaConfidence int,
	reg prometheus.Registerer,
	alphaConfidences []int,
) (Factory, error) {
	metrics, err := newEarlyTermNoTraversalMetrics(reg)
	if err != nil {
		return nil, err
	}

	return &earlyTermNoTraversalFactory{
		alphaPreference:  alphaPreference,
		alphaConfidence:  alphaConfidence,
		metrics:          metrics,
		alphaConfidences: alphaConfidences,
	}, nil
}

func (f *earlyTermNoTraversalFactory) New(vdrs bag.Bag[ids.NodeID]) Poll {
	return &earlyTermNoTraversalPoll{
		polled:          vdrs,
		alphaPreference: f.alphaPreference,
		alphaConfidence: f.alphaConfidence,
		metrics:         f.metrics,
		start:           time.Now(),
		confidences:     f.alphaConfidences,
	}
}

// earlyTermNoTraversalPoll finishes when any remaining validators can't change
// the result of the poll. However, does not terminate tightly with this bound.
// It terminates as quickly as it can without performing any DAG traversals.
type earlyTermNoTraversalPoll struct {
	votes           bag.Bag[ids.ID]
	polled          bag.Bag[ids.NodeID]
	alphaPreference int
	alphaConfidence int
	confidences     []int

	metrics  *earlyTermNoTraversalMetrics
	start    time.Time
	finished bool
}

// Vote registers a response for this poll
func (p *earlyTermNoTraversalPoll) Vote(vdr ids.NodeID, vote ids.ID) {
	count := p.polled.Count(vdr)
	// make sure that a validator can't respond multiple times
	p.polled.Remove(vdr)

	// track the votes the validator responded with
	p.votes.AddCount(vote, count)
}

// Drop any future response for this poll
func (p *earlyTermNoTraversalPoll) Drop(vdr ids.NodeID) {
	p.polled.Remove(vdr)
}

// Finished returns true when one of the following conditions is met.
//
//  1. There are no outstanding votes.
//  2. It is impossible for the poll to achieve an alphaPreference majority
//     after applying transitive voting.
//  3. A single element has achieved an alphaPreference majority and it is
//     impossible for it to achieve an alphaConfidence majority after applying
//     transitive voting.
//  4. A single element has achieved an alphaConfidence majority.
func (p *earlyTermNoTraversalPoll) Finished() bool {
	finished, _ := p.finishedAndReason()
	return finished
}
func (p *earlyTermNoTraversalPoll) finishedAndReason() (bool, int) {
	if p.finished {
		return true, 0
	}

	remaining := p.polled.Len()
	if remaining == 0 {
		p.finished = true
		p.metrics.observeExhausted(time.Since(p.start))
		return true, 1 // Case 1
	}

	received := p.votes.Len()
	maxPossibleVotes := received + remaining
	if maxPossibleVotes < p.alphaPreference {
		p.finished = true
		p.metrics.observeEarlyFail(time.Since(p.start))
		return true, 2 // Case 2
	}

	_, freq := p.votes.Mode()

	if len(p.confidences) > 0 {
		if fin, reason := p.shouldTerminateEarlyErrDriven(freq, maxPossibleVotes); fin {
			p.finished = true
			return true, reason
		}
		return false, 0
	}

	if freq >= p.alphaPreference && maxPossibleVotes < p.alphaConfidence {
		p.finished = true
		p.metrics.observeEarlyAlphaPref(time.Since(p.start))
		return true, 3 // Case 3
	}

	if freq >= p.alphaConfidence {
		p.finished = true
		p.metrics.observeEarlyAlphaConf(time.Since(p.start))
		return true, 4 // Case 4
	}

	return false, 0
}

func (p *earlyTermNoTraversalPoll) shouldTerminateEarlyErrDriven(freq, maxPossibleVotes int) (bool, int) {
	// Case 4 - First check if we collected the highest alpha confidence
	if freq >= p.confidences[len(p.confidences)-1] {
		p.metrics.observeEarlyAlphaConf(time.Since(p.start))
		return true, 4
	}

	if freq < p.alphaPreference {
		return false, 0
	}

	// Case 3a: We have collected the maximum votes, but it is below any of the confidence thresholds.
	if maxPossibleVotes < p.confidences[0] {
		p.metrics.observeEarlyAlphaPref(time.Since(p.start))
		return true, 3
	}

	// Case 3b - We don't have an outstanding query response that improves our confidence,
	// because we have collected a threshold below a threshold we cannot pass due to reaching maximum possible votes.

	for i := 0; i < len(p.confidences)-1; i++ {
		if freq >= p.confidences[i] && maxPossibleVotes < p.confidences[i+1] {
			p.metrics.observeEarlyAlphaPref(time.Since(p.start))
			return true, 3
		}
	}

	return false, 0
}

// Result returns the result of this poll
func (p *earlyTermNoTraversalPoll) Result() bag.Bag[ids.ID] {
	return p.votes
}

func (p *earlyTermNoTraversalPoll) PrefixedString(prefix string) string {
	return fmt.Sprintf(
		"waiting on %s\n%sreceived %s",
		p.polled.PrefixedString(prefix),
		prefix,
		p.votes.PrefixedString(prefix),
	)
}

func (p *earlyTermNoTraversalPoll) String() string {
	return p.PrefixedString("")
}
