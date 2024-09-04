// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package snowball

import "github.com/ava-labs/avalanchego/ids"

var (
	SnowballFactoryInstance Factory = SnowballFactory{}
	SnowflakeFactory        Factory = snowflakeFactory{}
)

type SnowballFactory struct {
	Interceptor func(Unary) Unary
}

func (SnowballFactory) NewNnary(params Parameters, choice ids.ID) Nnary {
	sb := newNnarySnowball(params.AlphaPreference, newSingleTerminationCondition(params.AlphaConfidence, params.Beta), choice)
	return &sb
}

func (s SnowballFactory) NewUnary(params Parameters) Unary {
	sb := newUnarySnowball(params.AlphaPreference, newSingleTerminationCondition(params.AlphaConfidence, params.Beta))
	if s.Interceptor != nil {
		return s.Interceptor(&sb)
	}
	return &sb
}

type snowflakeFactory struct{}

func (snowflakeFactory) NewNnary(params Parameters, choice ids.ID) Nnary {
	sf := newNnarySnowflake(params.AlphaPreference, newSingleTerminationCondition(params.AlphaConfidence, params.Beta), choice)
	return &sf
}

func (snowflakeFactory) NewUnary(params Parameters) Unary {
	sf := newUnarySnowflake(params.AlphaPreference, newSingleTerminationCondition(params.AlphaConfidence, params.Beta))
	return &sf
}
