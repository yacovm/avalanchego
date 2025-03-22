// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package handler

import (
	"errors"
	"github.com/ava-labs/avalanchego/utils/metric"

	"github.com/prometheus/client_golang/prometheus"
)

type metrics struct {
	expired             *prometheus.CounterVec // op
	messages            *prometheus.CounterVec // op
	lockingTime         prometheus.Gauge
	messageHandlingTime *prometheus.GaugeVec // op
	processingTimePut   metric.Averager
	processingTimeChits metric.Averager
}

func newMetrics(reg prometheus.Registerer) (*metrics, error) {
	processingTimePut, err := metric.NewAverager(
		"processing_time_put",
		"time (in ns) of a processing a put message",
		reg)
	if err != nil {
		return nil, err
	}

	processingTimeChits, err := metric.NewAverager(
		"processing_time_chits",
		"time (in ns) of a processing a chits message",
		reg)

	m := &metrics{
		processingTimePut:   processingTimePut,
		processingTimeChits: processingTimeChits,
		expired: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "expired",
				Help: "messages dropped because the deadline expired",
			},
			opLabels,
		),
		messages: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "messages",
				Help: "messages handled",
			},
			opLabels,
		),
		messageHandlingTime: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "message_handling_time",
				Help: "time spent handling messages",
			},
			opLabels,
		),
		lockingTime: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "locking_time",
			Help: "time spent acquiring the context lock",
		}),
	}
	return m, errors.Join(
		reg.Register(m.expired),
		reg.Register(m.messages),
		reg.Register(m.messageHandlingTime),
		reg.Register(m.lockingTime),
	)
}
