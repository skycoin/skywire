// Code generated by mockery v0.0.0-dev. DO NOT EDIT.

package mocks

import (
	context "context"

	cipher "github.com/skycoin/skywire-utilities/pkg/cipher"

	mock "github.com/stretchr/testify/mock"
)

// ConnectFn is an autogenerated mock type for the ConnectFn type
type ConnectFn struct {
	mock.Mock
}

// Execute provides a mock function with given fields: _a0, _a1
func (_m *ConnectFn) Execute(_a0 context.Context, _a1 cipher.PubKey) error {
	ret := _m.Called(_a0, _a1)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, cipher.PubKey) error); ok {
		r0 = rf(_a0, _a1)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewConnectFn creates a new instance of ConnectFn. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewConnectFn(t interface {
	mock.TestingT
	Cleanup(func())
}) *ConnectFn {
	mock := &ConnectFn{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
