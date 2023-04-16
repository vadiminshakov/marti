// Code generated by mockery v2.20.0. DO NOT EDIT.

package mocks

import (
	big "math/big"

	mock "github.com/stretchr/testify/mock"
	entity "github.com/vadimInshakov/marti/entity"
)

// Detector is an autogenerated mock type for the Detector type
type Detector struct {
	mock.Mock
}

// NeedAction provides a mock function with given fields: pair, price
func (_m *Detector) NeedAction(pair entity.Pair, price *big.Float) (entity.Action, error) {
	ret := _m.Called(pair, price)

	var r0 entity.Action
	var r1 error
	if rf, ok := ret.Get(0).(func(entity.Pair, *big.Float) (entity.Action, error)); ok {
		return rf(pair, price)
	}
	if rf, ok := ret.Get(0).(func(entity.Pair, *big.Float) entity.Action); ok {
		r0 = rf(pair, price)
	} else {
		r0 = ret.Get(0).(entity.Action)
	}

	if rf, ok := ret.Get(1).(func(entity.Pair, *big.Float) error); ok {
		r1 = rf(pair, price)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

type mockConstructorTestingTNewDetector interface {
	mock.TestingT
	Cleanup(func())
}

// NewDetector creates a new instance of Detector. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewDetector(t mockConstructorTestingTNewDetector) *Detector {
	mock := &Detector{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}