// Code generated by mockery v0.0.0-dev. DO NOT EDIT.

package mocks

import (
	routing "github.com/skycoin/skywire/pkg/routing"
	mock "github.com/stretchr/testify/mock"
)

// Table is an autogenerated mock type for the Table type
type Table struct {
	mock.Mock
}

// AllRules provides a mock function with given fields:
func (_m *Table) AllRules() []routing.Rule {
	ret := _m.Called()

	var r0 []routing.Rule
	if rf, ok := ret.Get(0).(func() []routing.Rule); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]routing.Rule)
		}
	}

	return r0
}

// CollectGarbage provides a mock function with given fields:
func (_m *Table) CollectGarbage() []routing.Rule {
	ret := _m.Called()

	var r0 []routing.Rule
	if rf, ok := ret.Get(0).(func() []routing.Rule); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]routing.Rule)
		}
	}

	return r0
}

// Count provides a mock function with given fields:
func (_m *Table) Count() int {
	ret := _m.Called()

	var r0 int
	if rf, ok := ret.Get(0).(func() int); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(int)
	}

	return r0
}

// DelRules provides a mock function with given fields: _a0
func (_m *Table) DelRules(_a0 []routing.RouteID) {
	_m.Called(_a0)
}

// ReserveKeys provides a mock function with given fields: n
func (_m *Table) ReserveKeys(n int) ([]routing.RouteID, error) {
	ret := _m.Called(n)

	var r0 []routing.RouteID
	var r1 error
	if rf, ok := ret.Get(0).(func(int) ([]routing.RouteID, error)); ok {
		return rf(n)
	}
	if rf, ok := ret.Get(0).(func(int) []routing.RouteID); ok {
		r0 = rf(n)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]routing.RouteID)
		}
	}

	if rf, ok := ret.Get(1).(func(int) error); ok {
		r1 = rf(n)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// Rule provides a mock function with given fields: _a0
func (_m *Table) Rule(_a0 routing.RouteID) (routing.Rule, error) {
	ret := _m.Called(_a0)

	var r0 routing.Rule
	var r1 error
	if rf, ok := ret.Get(0).(func(routing.RouteID) (routing.Rule, error)); ok {
		return rf(_a0)
	}
	if rf, ok := ret.Get(0).(func(routing.RouteID) routing.Rule); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(routing.Rule)
		}
	}

	if rf, ok := ret.Get(1).(func(routing.RouteID) error); ok {
		r1 = rf(_a0)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// RulesWithDesc provides a mock function with given fields: _a0
func (_m *Table) RulesWithDesc(_a0 routing.RouteDescriptor) []routing.Rule {
	ret := _m.Called(_a0)

	var r0 []routing.Rule
	if rf, ok := ret.Get(0).(func(routing.RouteDescriptor) []routing.Rule); ok {
		r0 = rf(_a0)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).([]routing.Rule)
		}
	}

	return r0
}

// SaveRule provides a mock function with given fields: _a0
func (_m *Table) SaveRule(_a0 routing.Rule) error {
	ret := _m.Called(_a0)

	var r0 error
	if rf, ok := ret.Get(0).(func(routing.Rule) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// UpdateActivity provides a mock function with given fields: _a0
func (_m *Table) UpdateActivity(_a0 routing.RouteID) error {
	ret := _m.Called(_a0)

	var r0 error
	if rf, ok := ret.Get(0).(func(routing.RouteID) error); ok {
		r0 = rf(_a0)
	} else {
		r0 = ret.Error(0)
	}

	return r0
}

// NewTable creates a new instance of Table. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewTable(t interface {
	mock.TestingT
	Cleanup(func())
}) *Table {
	mock := &Table{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
