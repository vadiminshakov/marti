// Code generated by mockery v2.20.0. DO NOT EDIT.

package mocks

import (
	decimal "github.com/shopspring/decimal"
	entity "github.com/vadimInshakov/marti/entity"

	mock "github.com/stretchr/testify/mock"
)

// Pricer is an autogenerated mock type for the Pricer type
type Pricer struct {
	mock.Mock
}

// GetPrice provides a mock function with given fields: pair
func (_m *Pricer) GetPrice(pair entity.Pair) (decimal.Decimal, error) {
	ret := _m.Called(pair)

	var r0 decimal.Decimal
	var r1 error
	if rf, ok := ret.Get(0).(func(entity.Pair) (decimal.Decimal, error)); ok {
		return rf(pair)
	}
	if rf, ok := ret.Get(0).(func(entity.Pair) decimal.Decimal); ok {
		r0 = rf(pair)
	} else {
		r0 = ret.Get(0).(decimal.Decimal)
	}

	if rf, ok := ret.Get(1).(func(entity.Pair) error); ok {
		r1 = rf(pair)
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

type mockConstructorTestingTNewPricer interface {
	mock.TestingT
	Cleanup(func())
}

// NewPricer creates a new instance of Pricer. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewPricer(t mockConstructorTestingTNewPricer) *Pricer {
	mock := &Pricer{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}