// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package utils

import (
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

func getStacktrace(all bool) string {
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, all)
	return string(buf[:n])
}

func GetStacktrace(all bool) string {
	return globalFuncDecorator.GetStackTrace()
}

var globalFuncDecorator FuncDecorator

func DecorateAndExecute(f func(), name string) {
	globalFuncDecorator.run(f, name)
}

type FuncDecorator struct {
	lock sync.Mutex
	m    map[uint64]string
}

func (fd *FuncDecorator) GetStackTrace() string {
	s := getStacktrace(true)
	buff := make([]byte, 0, len(s)+100)
	bb := bytes.NewBuffer(buff)

	label := "(*FuncDecorator).f"

	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, label) {
			// Get the number
			index := strings.Index(line, label)
			if index != -1 {
				start := index + len(label)
				end := start
				for end < len(line) {
					if line[end] < '0' || line[end] > '9' {
						break
					}
					end++
				}
				id, err := strconv.ParseInt(string(line[start:end]), 10, 32)
				if err == nil {
					fd.lock.Lock()
					tag, ok := fd.m[uint64(id)]
					fd.lock.Unlock()
					if ok {
						line = line + " [" + tag + "]"
					}
				}
			}
		}
		bb.WriteString(line)
		bb.Write([]byte("\n"))
	}

	return bb.String()
}

func (fd *FuncDecorator) run(f func(), name string) {
	id := fd.decorateFunctionCall(name)

	switch id {
	case 0:
		fd.f0(f)
	case 1:
		fd.f1(f)
	case 2:
		fd.f2(f)
	case 3:
		fd.f3(f)
	case 4:
		fd.f4(f)
	case 5:
		fd.f5(f)
	case 6:
		fd.f6(f)
	case 7:
		fd.f7(f)
	case 8:
		fd.f8(f)
	case 9:
		fd.f9(f)
	case 10:
		fd.f10(f)
	case 11:
		fd.f11(f)
	case 12:
		fd.f12(f)
	case 13:
		fd.f13(f)
	case 14:
		fd.f14(f)
	case 15:
		fd.f15(f)
	case 16:
		fd.f16(f)
	case 17:
		fd.f17(f)
	case 18:
		fd.f18(f)
	case 19:
		fd.f19(f)
	case 20:
		fd.f20(f)
	case 21:
		fd.f21(f)
	case 22:
		fd.f22(f)
	case 23:
		fd.f23(f)
	case 24:
		fd.f24(f)
	case 25:
		fd.f25(f)
	case 26:
		fd.f26(f)
	case 27:
		fd.f27(f)
	case 28:
		fd.f28(f)
	case 29:
		fd.f29(f)
	case 30:
		fd.f30(f)
	case 31:
		fd.f31(f)
	case 32:
		fd.f32(f)
	case 33:
		fd.f33(f)
	case 34:
		fd.f34(f)
	case 35:
		fd.f35(f)
	case 36:
		fd.f36(f)
	case 37:
		fd.f37(f)
	case 38:
		fd.f38(f)
	case 39:
		fd.f39(f)
	case 40:
		fd.f40(f)
	case 41:
		fd.f41(f)
	case 42:
		fd.f42(f)
	case 43:
		fd.f43(f)
	case 44:
		fd.f44(f)
	case 45:
		fd.f45(f)
	case 46:
		fd.f46(f)
	case 47:
		fd.f47(f)
	case 48:
		fd.f48(f)
	case 49:
		fd.f49(f)
	case 50:
		fd.f50(f)
	case 51:
		fd.f51(f)
	case 52:
		fd.f52(f)
	case 53:
		fd.f53(f)
	case 54:
		fd.f54(f)
	case 55:
		fd.f55(f)
	case 56:
		fd.f56(f)
	case 57:
		fd.f57(f)
	case 58:
		fd.f58(f)
	case 59:
		fd.f59(f)
	case 60:
		fd.f60(f)
	case 61:
		fd.f61(f)
	case 62:
		fd.f62(f)
	case 63:
		fd.f63(f)
	case 64:
		fd.f64(f)
	case 65:
		fd.f65(f)
	case 66:
		fd.f66(f)
	case 67:
		fd.f67(f)
	case 68:
		fd.f68(f)
	case 69:
		fd.f69(f)
	case 70:
		fd.f70(f)
	case 71:
		fd.f71(f)
	case 72:
		fd.f72(f)
	case 73:
		fd.f73(f)
	case 74:
		fd.f74(f)
	case 75:
		fd.f75(f)
	case 76:
		fd.f76(f)
	case 77:
		fd.f77(f)
	case 78:
		fd.f78(f)
	case 79:
		fd.f79(f)
	case 80:
		fd.f80(f)
	case 81:
		fd.f81(f)
	case 82:
		fd.f82(f)
	case 83:
		fd.f83(f)
	case 84:
		fd.f84(f)
	case 85:
		fd.f85(f)
	case 86:
		fd.f86(f)
	case 87:
		fd.f87(f)
	case 88:
		fd.f88(f)
	case 89:
		fd.f89(f)
	case 90:
		fd.f90(f)
	case 91:
		fd.f91(f)
	case 92:
		fd.f92(f)
	case 93:
		fd.f93(f)
	case 94:
		fd.f94(f)
	case 95:
		fd.f95(f)
	case 96:
		fd.f96(f)
	case 97:
		fd.f97(f)
	case 98:
		fd.f98(f)
	case 99:
		fd.f99(f)
	}
}

func (fd *FuncDecorator) decorateFunctionCall(name string) int {
	fd.lock.Lock()
	defer fd.lock.Unlock()

	if fd.m == nil {
		fd.m = make(map[uint64]string)
	}

	id := len(fd.m)
	fd.m[uint64(id)] = name

	return id
}

func (fd *FuncDecorator) f0(f func()) {
	f()
}

func (fd *FuncDecorator) f1(f func()) {
	f()
}

func (fd *FuncDecorator) f2(f func()) {
	f()
}

func (fd *FuncDecorator) f3(f func()) {
	f()
}

func (fd *FuncDecorator) f4(f func()) {
	f()
}

func (fd *FuncDecorator) f5(f func()) {
	f()
}

func (fd *FuncDecorator) f6(f func()) {
	f()
}

func (fd *FuncDecorator) f7(f func()) {
	f()
}

func (fd *FuncDecorator) f8(f func()) {
	f()
}

func (fd *FuncDecorator) f9(f func()) {
	f()
}

func (fd *FuncDecorator) f10(f func()) {
	f()
}

func (fd *FuncDecorator) f11(f func()) {
	f()
}

func (fd *FuncDecorator) f12(f func()) {
	f()
}

func (fd *FuncDecorator) f13(f func()) {
	f()
}

func (fd *FuncDecorator) f14(f func()) {
	f()
}

func (fd *FuncDecorator) f15(f func()) {
	f()
}

func (fd *FuncDecorator) f16(f func()) {
	f()
}

func (fd *FuncDecorator) f17(f func()) {
	f()
}

func (fd *FuncDecorator) f18(f func()) {
	f()
}

func (fd *FuncDecorator) f19(f func()) {
	f()
}

func (fd *FuncDecorator) f20(f func()) {
	f()
}

func (fd *FuncDecorator) f21(f func()) {
	f()
}

func (fd *FuncDecorator) f22(f func()) {
	f()
}

func (fd *FuncDecorator) f23(f func()) {
	f()
}

func (fd *FuncDecorator) f24(f func()) {
	f()
}

func (fd *FuncDecorator) f25(f func()) {
	f()
}

func (fd *FuncDecorator) f26(f func()) {
	f()
}

func (fd *FuncDecorator) f27(f func()) {
	f()
}

func (fd *FuncDecorator) f28(f func()) {
	f()
}

func (fd *FuncDecorator) f29(f func()) {
	f()
}

func (fd *FuncDecorator) f30(f func()) {
	f()
}

func (fd *FuncDecorator) f31(f func()) {
	f()
}

func (fd *FuncDecorator) f32(f func()) {
	f()
}

func (fd *FuncDecorator) f33(f func()) {
	f()
}

func (fd *FuncDecorator) f34(f func()) {
	f()
}

func (fd *FuncDecorator) f35(f func()) {
	f()
}

func (fd *FuncDecorator) f36(f func()) {
	f()
}

func (fd *FuncDecorator) f37(f func()) {
	f()
}

func (fd *FuncDecorator) f38(f func()) {
	f()
}

func (fd *FuncDecorator) f39(f func()) {
	f()
}

func (fd *FuncDecorator) f40(f func()) {
	f()
}

func (fd *FuncDecorator) f41(f func()) {
	f()
}

func (fd *FuncDecorator) f42(f func()) {
	f()
}

func (fd *FuncDecorator) f43(f func()) {
	f()
}

func (fd *FuncDecorator) f44(f func()) {
	f()
}

func (fd *FuncDecorator) f45(f func()) {
	f()
}

func (fd *FuncDecorator) f46(f func()) {
	f()
}

func (fd *FuncDecorator) f47(f func()) {
	f()
}

func (fd *FuncDecorator) f48(f func()) {
	f()
}

func (fd *FuncDecorator) f49(f func()) {
	f()
}

func (fd *FuncDecorator) f50(f func()) {
	f()
}

func (fd *FuncDecorator) f51(f func()) {
	f()
}

func (fd *FuncDecorator) f52(f func()) {
	f()
}

func (fd *FuncDecorator) f53(f func()) {
	f()
}

func (fd *FuncDecorator) f54(f func()) {
	f()
}

func (fd *FuncDecorator) f55(f func()) {
	f()
}

func (fd *FuncDecorator) f56(f func()) {
	f()
}

func (fd *FuncDecorator) f57(f func()) {
	f()
}

func (fd *FuncDecorator) f58(f func()) {
	f()
}

func (fd *FuncDecorator) f59(f func()) {
	f()
}

func (fd *FuncDecorator) f60(f func()) {
	f()
}

func (fd *FuncDecorator) f61(f func()) {
	f()
}

func (fd *FuncDecorator) f62(f func()) {
	f()
}

func (fd *FuncDecorator) f63(f func()) {
	f()
}

func (fd *FuncDecorator) f64(f func()) {
	f()
}

func (fd *FuncDecorator) f65(f func()) {
	f()
}

func (fd *FuncDecorator) f66(f func()) {
	f()
}

func (fd *FuncDecorator) f67(f func()) {
	f()
}

func (fd *FuncDecorator) f68(f func()) {
	f()
}

func (fd *FuncDecorator) f69(f func()) {
	f()
}

func (fd *FuncDecorator) f70(f func()) {
	f()
}

func (fd *FuncDecorator) f71(f func()) {
	f()
}

func (fd *FuncDecorator) f72(f func()) {
	f()
}

func (fd *FuncDecorator) f73(f func()) {
	f()
}

func (fd *FuncDecorator) f74(f func()) {
	f()
}

func (fd *FuncDecorator) f75(f func()) {
	f()
}

func (fd *FuncDecorator) f76(f func()) {
	f()
}

func (fd *FuncDecorator) f77(f func()) {
	f()
}

func (fd *FuncDecorator) f78(f func()) {
	f()
}

func (fd *FuncDecorator) f79(f func()) {
	f()
}

func (fd *FuncDecorator) f80(f func()) {
	f()
}

func (fd *FuncDecorator) f81(f func()) {
	f()
}

func (fd *FuncDecorator) f82(f func()) {
	f()
}

func (fd *FuncDecorator) f83(f func()) {
	f()
}

func (fd *FuncDecorator) f84(f func()) {
	f()
}

func (fd *FuncDecorator) f85(f func()) {
	f()
}

func (fd *FuncDecorator) f86(f func()) {
	f()
}

func (fd *FuncDecorator) f87(f func()) {
	f()
}

func (fd *FuncDecorator) f88(f func()) {
	f()
}

func (fd *FuncDecorator) f89(f func()) {
	f()
}

func (fd *FuncDecorator) f90(f func()) {
	f()
}

func (fd *FuncDecorator) f91(f func()) {
	f()
}

func (fd *FuncDecorator) f92(f func()) {
	f()
}

func (fd *FuncDecorator) f93(f func()) {
	f()
}

func (fd *FuncDecorator) f94(f func()) {
	f()
}

func (fd *FuncDecorator) f95(f func()) {
	f()
}

func (fd *FuncDecorator) f96(f func()) {
	f()
}

func (fd *FuncDecorator) f97(f func()) {
	f()
}

func (fd *FuncDecorator) f98(f func()) {
	f()
}

func (fd *FuncDecorator) f99(f func()) {
	f()
}
