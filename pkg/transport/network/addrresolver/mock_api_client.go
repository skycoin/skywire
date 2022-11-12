// Code generated by mockery v1.0.0. DO NOT EDIT.

package addrresolver

import (
	context "context"

	pfilter "github.com/AudriusButkevicius/pfilter"
	mock "github.com/stretchr/testify/mock"

	cipher "github.com/skycoin/skywire-utilities/pkg/cipher"
)

// MockAPIClient is an autogenerated mock type for the APIClient type
type MockAPIClient struct {
	mock.Mock
}

// BindSTCPR provides a mock function with given fields: ctx, port
func (_m *MockAPIClient) BindSTCPR(ctx context.Context, port string) error {
	ret := _m.Called(ctx, port)

	var r0 error
	if rf, ok := ret.Get(0).(func(context.Context, string) error); ok {
		r0 = rf(ctx, port)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// BindSUDPH provides a mock function with given fields: filter, handshake
func (_m *MockAPIClient) BindSUDPH(filter *pfilter.PacketFilter, handshake Handshake) (<-chan RemoteVisor, error) {
	ret := _m.Called(filter, handshake)

	var r0 <-chan RemoteVisor
	if rf, ok := ret.Get(0).(func(*pfilter.PacketFilter, Handshake) <-chan RemoteVisor); ok {
		r0 = rf(filter, handshake)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(<-chan RemoteVisor)
		}
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(*pfilter.PacketFilter, Handshake) error); ok {
		r1 = rf(filter, handshake)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Close provides a mock function with given fields:
func (_m *MockAPIClient) Close() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// Health provides a mock function with given fields: ctx
func (_m *MockAPIClient) Health(ctx context.Context) (int, error) {
	ret := _m.Called(ctx)

	var r0 int
	if rf, ok := ret.Get(0).(func(context.Context) int); ok {
		r0 = rf(ctx)
	} else {
		r0 = ret.Get(0).(int)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context) error); ok {
		r1 = rf(ctx)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Resolve provides a mock function with given fields: ctx, netType, pk
func (_m *MockAPIClient) Resolve(ctx context.Context, netType string, pk cipher.PubKey) (VisorData, error) {
	ret := _m.Called(ctx, netType, pk)

	var r0 VisorData
	if rf, ok := ret.Get(0).(func(context.Context, string, cipher.PubKey) VisorData); ok {
		r0 = rf(ctx, netType, pk)
	} else {
		r0 = ret.Get(0).(VisorData)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func(context.Context, string, cipher.PubKey) error); ok {
		r1 = rf(ctx, netType, pk)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

func (_m *MockAPIClient)Addresses(ctx context.Context)(string,error) {
	return "",nil
}
