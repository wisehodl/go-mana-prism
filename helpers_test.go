package prism

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

const (
	TestTimeout         = 2 * time.Second
	TestTick            = 10 * time.Millisecond
	NegativeTestTimeout = 100 * time.Millisecond
)

func Eventually(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	assert.Eventually(t, condition, TestTimeout, TestTick, msg)
}

func Never(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	assert.Never(t, condition, NegativeTestTimeout, TestTick, msg)
}
