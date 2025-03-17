// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bench

import (
	"sync"
	"time"
)

type Lock struct {
	innerLock sync.Locker
	report    func(duration time.Duration)
	lastLock  time.Time
}

func NewLock(innerLock sync.Locker, report func(duration time.Duration)) *Lock {
	return &Lock{innerLock: innerLock, report: report}
}

func (l *Lock) Lock() {
	l.innerLock.Lock()
	l.lastLock = time.Now()
}

func (l *Lock) Unlock() {
	l.report(time.Since(l.lastLock))
	l.innerLock.Unlock()
}
