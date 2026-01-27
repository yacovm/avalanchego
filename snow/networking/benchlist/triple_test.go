// Copyright (C) 2019, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package benchlist

import (
	"crypto/rand"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)



func TestTriple(t *testing.T) {
	type triple struct {
		a, b uint16
		c uint32
	}

	buff := make([]byte, 8)
	_, err := rand.Read(buff)
	require.NoError(t, err)

	testTriple := triple{
		a: binary.BigEndian.Uint16(buff[:2]),
		b: binary.BigEndian.Uint16(buff[2:4]),
		c: binary.BigEndian.Uint32(buff[4:]),
	}

	var at atomicTriple
	at.store(testTriple.a, testTriple.b, testTriple.c)
	a, b, c := at.load()
	require.Equal(t, testTriple.a, a)
	require.Equal(t, testTriple.b, b)
	require.Equal(t, testTriple.c, c)

	at.incA()
	at.incB()

	a, b, c = at.load()
	require.Equal(t, testTriple.a+1, a)
	require.Equal(t, testTriple.b+1, b)
	require.Equal(t, testTriple.c, c)
}
