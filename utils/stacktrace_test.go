// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import (
	"fmt"
	"sync"
	"testing"
)

/*	//		fmt.Printf("\tcase %d:\n\t\tfd.f%d(f)\n", i, i)
	//		fmt.Printf("func (fd *FuncDecorator) f%d(f func()) {\n\tf()\n}\n\n", i)

	for i := 0; i < 100; i++ {
		fmt.Printf("func (fd *FuncDecorator) f%d(f func()) {\n\tf()\n}\n\n", i)

	}*/

func TestGetStacktrace(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	done := make(chan struct{})
	quit := make(chan struct{})

	f := func() {
		close(quit)
		<-done
	}

	var fd FuncDecorator

	go func() {
		defer wg.Done()
		fd.run(f, "tagged")
	}()

	<-quit
	fmt.Println(fd.GetStackTrace())
	close(done)

	wg.Wait()
}
