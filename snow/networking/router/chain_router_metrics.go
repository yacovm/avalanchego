// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package router

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// routerMetrics about router messages
type routerMetrics struct {
	outstandingRequests   prometheus.Gauge
	longestRunningRequest prometheus.Gauge
	droppedRequests       prometheus.Counter
	timeouts              prometheus.Counter
	issuedRequests        prometheus.Counter
}

func newRouterMetrics(registerer prometheus.Registerer) (*routerMetrics, error) {
	rMetrics := &routerMetrics{}
	rMetrics.issuedRequests = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "issued_requests",
		Help: "Number of requests we have sent",
	})
	rMetrics.timeouts = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "request_timeouts",
		Help: "Number of times a timeout occurred on a query",
	})
	rMetrics.outstandingRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "outstanding",
			Help: "Number of outstanding requests (all types)",
		},
	)
	rMetrics.longestRunningRequest = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "longest_running",
			Help: "Time (in ns) the longest request took",
		},
	)
	rMetrics.droppedRequests = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "dropped",
			Help: "Number of dropped requests (all types)",
		},
	)

	err := errors.Join(
		registerer.Register(rMetrics.outstandingRequests),
		registerer.Register(rMetrics.longestRunningRequest),
		registerer.Register(rMetrics.droppedRequests),
		registerer.Register(rMetrics.timeouts),
		registerer.Register(rMetrics.issuedRequests),
	)
	return rMetrics, err
}
