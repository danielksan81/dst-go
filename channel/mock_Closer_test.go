// Code generated by mockery v1.0.0. DO NOT EDIT.

package channel

import mock "github.com/stretchr/testify/mock"

// MockCloser is an autogenerated mock type for the Closer type
type MockCloser struct {
	mock.Mock
}

// Close provides a mock function with given fields:
func (_m *MockCloser) Close() error {
	ret := _m.Called()

	var r0 error
	if rf, ok := ret.Get(0).(func() error); ok {
		r0 = rf()
	} else {
		r0 = ret.Error(0)
	}

	return r0
}
