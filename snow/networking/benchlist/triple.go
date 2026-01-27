// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import "sync/atomic"

// atomicTriple is a type used to atomically store and manipulate three independent values: two uint16 and one uint32.
type atomicTriple uint64

func (at *atomicTriple) load() (uint16, uint16, uint32) {
	val := atomic.LoadUint64((*uint64)(at))
	a := uint16(val >> 48)
	b := uint16(val >> 32)
	c := uint32(val)
	return a, b, c
}

func (at *atomicTriple) incA() {
	atomic.AddUint64((*uint64)(at), 1<<48)
}

func (at *atomicTriple) incB() {
	atomic.AddUint64((*uint64)(at), 1<<32)
}

func (at *atomicTriple) store(a, b uint16, c uint32) {
	var val uint64
	val |= uint64(a) << 48
	val |= uint64(b) << 32
	val |= uint64(c)
	atomic.StoreUint64((*uint64)(at), val)
}
