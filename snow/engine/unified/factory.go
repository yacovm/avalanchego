// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package unified

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/engine/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/bootstrap"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/syncer"
	"github.com/ava-labs/avalanchego/trace"
	"github.com/ava-labs/avalanchego/utils/logging"

	avbootstrap "github.com/ava-labs/avalanchego/snow/engine/avalanche/bootstrap"
)

type EngineFactory struct {
	TracingEnabled    bool
	Tracer            trace.Tracer
	StateSync         bool
	Logger            logging.Logger
	StateSyncConfig   syncer.Config
	BootConfig        bootstrap.Config
	SnowmanConfig     snowman.Config
	GetServer         common.AllGetsServer
	AvaBootConfig     avbootstrap.Config
	AvaAncestorGetter common.GetAncestorsHandler
	AvaMetrics        prometheus.Registerer
}

func (ef *EngineFactory) ClearBootstrapDB() error {
	return database.AtomicClear(ef.BootConfig.DB, ef.BootConfig.DB)
}

func (ef *EngineFactory) NewAvalancheAncestorsGetter() common.GetAncestorsHandler {
	return ef.AvaAncestorGetter
}

func (ef *EngineFactory) AllGetServer() common.AllGetsServer {
	return ef.GetServer
}

func (ef *EngineFactory) HasStateSync() bool {
	return ef.StateSync
}

func (ef *EngineFactory) NewStateSyncer(f OnFinishedFunc) (common.StateSyncer, error) {
	stateSyncer := syncer.New(ef.StateSyncConfig, f)

	if ef.TracingEnabled {
		stateSyncer = &tracedStateSyncer{
			Enabler:     common.NewTracedIsEnabled(stateSyncer, ef.Tracer),
			StateSyncer: stateSyncer,
		}
	}

	return stateSyncer, nil
}

func (ef *EngineFactory) NewAvalancheSyncer(f OnFinishedFunc) (common.AvalancheBootstrapableEngine, error) {
	var avalancheBootstrapper common.AvalancheBootstrapableEngine
	var err error

	avalancheBootstrapper, err = avbootstrap.New(
		ef.AvaBootConfig,
		f,
		ef.AvaMetrics,
	)
	if err != nil {
		ef.Logger.Fatal("error initializing avalanche bootstrapper:", zap.Error(err))
		return nil, err
	}

	if ef.TracingEnabled {
		avalancheBootstrapper = &tracedClearer{
			Clearer:                      common.NewTracedClearer(avalancheBootstrapper, ef.Tracer),
			AvalancheBootstrapableEngine: avalancheBootstrapper,
		}
	}

	return avalancheBootstrapper, nil
}

func (ef *EngineFactory) NewSnowman() (common.ConsensusEngine, error) {
	snowmanEngine, err := snowman.New(ef.SnowmanConfig)
	if err != nil {
		ef.Logger.Fatal("error initializing snowman engine:", zap.Error(err))
		return nil, err
	}
	return snowmanEngine, nil
}

func (ef *EngineFactory) NewSnowBootstrapper(f OnFinishedFunc) (common.BootstrapableEngine, error) {
	var bootstrapper common.BootstrapableEngine
	var err error
	bootstrapper, err = bootstrap.New(
		ef.BootConfig,
		f,
	)
	if err != nil {
		ef.Logger.Fatal("error initializing snowman bootstrapper:", zap.Error(err))
		return nil, err
	}

	if ef.TracingEnabled {
		bootstrapper = &tracedClearer{
			Clearer:                      common.NewTracedClearer(bootstrapper, ef.Tracer),
			AvalancheBootstrapableEngine: bootstrapper,
			AcceptedHandler:              bootstrapper,
			AcceptedFrontierHandler:      bootstrapper,
		}
	}

	return bootstrapper, nil
}

type tracedStateSyncer struct {
	common.Enabler
	common.StateSyncer
}

func (tss *tracedStateSyncer) IsEnabled(ctx context.Context) (bool, error) {
	return tss.Enabler.IsEnabled(ctx)
}

type tracedClearer struct {
	common.Clearer
	common.AvalancheBootstrapableEngine
	common.AcceptedFrontierHandler
	common.AcceptedHandler
}

func (tc *tracedClearer) Clear(ctx context.Context) error {
	return tc.Clearer.Clear(ctx)
}