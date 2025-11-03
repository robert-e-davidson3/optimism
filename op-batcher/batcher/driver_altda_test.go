// Test the batcher's altda retry & fallback behavior.

package batcher

import (
	"testing"
	"time"

	"github.com/ethereum-optimism/optimism/op-batcher/config"
	"github.com/ethereum-optimism/optimism/op-batcher/metrics"
	"github.com/ethereum-optimism/optimism/op-service/testlog"
	"github.com/ethereum/go-ethereum/log"
	"github.com/stretchr/testify/require"
)

// setupWithAltDA creates a BatchSubmitter configured for AltDA testing
func setupWithAltDA(t *testing.T, failureThreshold uint64, retryInterval time.Duration) (*BatchSubmitter, *metrics.TestMetrics) {
	ep := newEndpointProvider()
	cfg := defaultTestRollupConfig
	cfg.Genesis.L1.Number = genesisL1Origin

	testMetrics := &metrics.TestMetrics{}

	bs := NewBatchSubmitter(DriverSetup{
		Log:          testlog.Logger(t, log.LevelDebug),
		Metr:         testMetrics,
		RollupConfig: cfg,
		Config: BatcherConfig{
			UseAltDA:                true,
			AltDAFailureThreshold:   failureThreshold,
			AltDARetryInterval:      retryInterval,
			MaxConcurrentDARequests: 1,
			ThrottleParams: config.ThrottleParams{
				ControllerType: config.StepControllerType,
			},
		},
		ChannelConfig:    defaultTestChannelConfig(),
		EndpointProvider: ep,
		AltDA:            nil, // Not needed for state management tests
	})

	// Initialize AltDA state
	bs.altDAState = AltDAGood
	bs.altDAFailureCount = 0
	bs.altDALastFailureTime = time.Time{}

	return bs, testMetrics
}

// TestCanUseAltDA verifies the canUseAltDA() method returns correct values
func TestCanUseAltDA(t *testing.T) {
	tests := []struct {
		name        string
		useAltDA    bool
		state       AltDAState
		expectUsage bool
	}{
		{
			name:        "UseAltDA enabled and Good state",
			useAltDA:    true,
			state:       AltDAGood,
			expectUsage: true,
		},
		{
			name:        "UseAltDA enabled and Retrying state",
			useAltDA:    true,
			state:       AltDARetrying,
			expectUsage: true,
		},
		{
			name:        "UseAltDA enabled but Degraded state",
			useAltDA:    true,
			state:       AltDADegraded,
			expectUsage: false,
		},
		{
			name:        "UseAltDA disabled and Good state",
			useAltDA:    false,
			state:       AltDAGood,
			expectUsage: false,
		},
		{
			name:        "UseAltDA disabled and Degraded state",
			useAltDA:    false,
			state:       AltDADegraded,
			expectUsage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, _ := setupWithAltDA(t, 5, 5*time.Minute)
			bs.Config.UseAltDA = tt.useAltDA
			bs.altDAState = tt.state

			result := bs.canUseAltDA()
			require.Equal(t, tt.expectUsage, result)
		})
	}
}

// TestRecordAltDASuccess verifies success handling
func TestRecordAltDASuccess(t *testing.T) {
	tests := []struct {
		name         string
		initialState AltDAState
		initialCount uint64
	}{
		{
			name:         "Recovery from Degraded",
			initialState: AltDADegraded,
			initialCount: 10,
		},
		{
			name:         "Recovery from Retrying",
			initialState: AltDARetrying,
			initialCount: 5,
		},
		{
			name:         "Maintain Good state",
			initialState: AltDAGood,
			initialCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, testMetrics := setupWithAltDA(t, 5, 5*time.Minute)
			bs.altDAState = tt.initialState
			bs.altDAFailureCount = tt.initialCount

			bs.recordAltDASuccess()

			require.Equal(t, AltDAGood, bs.altDAState)
			require.Equal(t, uint64(0), bs.altDAFailureCount)
			require.Equal(t, int(AltDAGood), testMetrics.AltDAState)
			require.Equal(t, uint64(0), testMetrics.AltDAFailureCount)
		})
	}
}

// TestRecordAltDAFailure verifies failure handling
func TestRecordAltDAFailure(t *testing.T) {
	tests := []struct {
		name          string
		threshold     uint64
		initialState  AltDAState
		initialCount  uint64
		expectedState AltDAState
		expectedCount uint64
	}{
		{
			name:          "First failure, stay Good",
			threshold:     5,
			initialState:  AltDAGood,
			initialCount:  0,
			expectedState: AltDAGood,
			expectedCount: 1,
		},
		{
			name:          "At threshold-1, stay Good",
			threshold:     5,
			initialState:  AltDAGood,
			initialCount:  3,
			expectedState: AltDAGood,
			expectedCount: 4,
		},
		{
			name:          "At threshold, transition to Degraded",
			threshold:     5,
			initialState:  AltDAGood,
			initialCount:  4,
			expectedState: AltDADegraded,
			expectedCount: 5,
		},
		{
			name:          "Already Degraded, increment count",
			threshold:     5,
			initialState:  AltDADegraded,
			initialCount:  10,
			expectedState: AltDADegraded,
			expectedCount: 11,
		},
		{
			name:          "Retrying fails, back to Degraded",
			threshold:     5,
			initialState:  AltDARetrying,
			initialCount:  5,
			expectedState: AltDADegraded,
			expectedCount: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bs, testMetrics := setupWithAltDA(t, tt.threshold, 5*time.Minute)
			bs.altDAState = tt.initialState
			bs.altDAFailureCount = tt.initialCount

			bs.recordAltDAFailure()

			require.Equal(t, tt.expectedState, bs.altDAState)
			require.Equal(t, tt.expectedCount, bs.altDAFailureCount)
			require.Equal(t, int(tt.expectedState), testMetrics.AltDAState)
			require.Equal(t, tt.expectedCount, testMetrics.AltDAFailureCount)
			require.False(t, bs.altDALastFailureTime.IsZero())
		})
	}
}

// TestAltDAFailureThreshold verifies the failure threshold behavior
// AKA reproduces going from Good to Degraded
func TestAltDAFailureThreshold(t *testing.T) {
	bs, testMetrics := setupWithAltDA(t, 3, 5*time.Minute)

	// Verify initial state
	require.Equal(t, AltDAGood, bs.altDAState)
	require.Equal(t, uint64(0), bs.altDAFailureCount)

	// Failure 1: Should stay Good
	bs.recordAltDAFailure()
	require.Equal(t, AltDAGood, bs.altDAState)
	require.Equal(t, uint64(1), bs.altDAFailureCount)
	require.Equal(t, int(AltDAGood), testMetrics.AltDAState)

	// Failure 2: Should stay Good
	bs.recordAltDAFailure()
	require.Equal(t, AltDAGood, bs.altDAState)
	require.Equal(t, uint64(2), bs.altDAFailureCount)

	// Failure 3 (at threshold): Should transition to Degraded
	bs.recordAltDAFailure()
	require.Equal(t, AltDADegraded, bs.altDAState)
	require.Equal(t, uint64(3), bs.altDAFailureCount)
	require.Equal(t, int(AltDADegraded), testMetrics.AltDAState)
	require.Equal(t, uint64(3), testMetrics.AltDAFailureCount)

	// Further failures should stay Degraded but increment count
	bs.recordAltDAFailure()
	require.Equal(t, AltDADegraded, bs.altDAState)
	require.Equal(t, uint64(4), bs.altDAFailureCount)
}

// TestAltDARetryBackoff verifies the retry backoff timing
func TestAltDARetryBackoff(t *testing.T) {
	retryInterval := 10 * time.Millisecond
	bs, testMetrics := setupWithAltDA(t, 3, retryInterval)

	// Set state to Degraded with a recent failure
	bs.altDAState = AltDADegraded
	bs.altDAFailureCount = 5
	bs.altDALastFailureTime = time.Now()

	// Should not retry immediately
	require.False(t, bs.shouldRetryAltDA())

	// Wait for retry interval to elapse
	time.Sleep(retryInterval + 10*time.Millisecond)

	// Should now be ready to retry
	require.True(t, bs.shouldRetryAltDA())

	// Transition to Retrying
	bs.transitionToRetrying()
	require.Equal(t, AltDARetrying, bs.altDAState)
	require.Equal(t, int(AltDARetrying), testMetrics.AltDAState)

	// shouldRetryAltDA should return false now (not in Degraded state)
	require.False(t, bs.shouldRetryAltDA())
}

// TestAltDAStateTransitions verifies all state transition paths
func TestAltDAStateTransitions(t *testing.T) {
	t.Run("Good -> Degraded", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 2, 5*time.Minute)
		require.Equal(t, AltDAGood, bs.altDAState)

		// 2 failures to hit threshold
		bs.recordAltDAFailure()
		bs.recordAltDAFailure()
		require.Equal(t, AltDADegraded, bs.altDAState)
	})

	t.Run("Degraded -> Retrying", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 3, 50*time.Millisecond)
		bs.altDAState = AltDADegraded
		bs.altDALastFailureTime = time.Now()

		// Wait for retry interval
		time.Sleep(60 * time.Millisecond)

		bs.transitionToRetrying()
		require.Equal(t, AltDARetrying, bs.altDAState)
	})

	t.Run("Retrying -> Good", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 3, 5*time.Minute)
		bs.altDAState = AltDARetrying
		bs.altDAFailureCount = 5

		bs.recordAltDASuccess()
		require.Equal(t, AltDAGood, bs.altDAState)
		require.Equal(t, uint64(0), bs.altDAFailureCount)
	})

	t.Run("Retrying -> Degraded", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 3, 5*time.Minute)
		bs.altDAState = AltDARetrying
		bs.altDAFailureCount = 5

		bs.recordAltDAFailure()
		require.Equal(t, AltDADegraded, bs.altDAState)
		require.Equal(t, uint64(6), bs.altDAFailureCount)
	})

	t.Run("Degraded -> Good (via Retrying)", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 3, 50*time.Millisecond)
		bs.altDAState = AltDADegraded
		bs.altDAFailureCount = 10
		bs.altDALastFailureTime = time.Now()

		// Wait and transition to Retrying
		time.Sleep(60 * time.Millisecond)
		bs.transitionToRetrying()
		require.Equal(t, AltDARetrying, bs.altDAState)

		// Success brings back to Good
		bs.recordAltDASuccess()
		require.Equal(t, AltDAGood, bs.altDAState)
		require.Equal(t, uint64(0), bs.altDAFailureCount)
	})
}

// TestAltDAMetrics verifies metrics are correctly recorded
func TestAltDAMetrics(t *testing.T) {
	bs, testMetrics := setupWithAltDA(t, 3, 5*time.Minute)

	// Initial state
	require.Equal(t, 0, testMetrics.AltDAState)
	require.Equal(t, uint64(0), testMetrics.AltDAFailureCount)

	// Record failure
	bs.recordAltDAFailure()
	require.Equal(t, int(AltDAGood), testMetrics.AltDAState) // Still Good, under threshold
	require.Equal(t, uint64(1), testMetrics.AltDAFailureCount)

	// Hit threshold
	bs.recordAltDAFailure()
	bs.recordAltDAFailure()
	require.Equal(t, int(AltDADegraded), testMetrics.AltDAState)
	require.Equal(t, uint64(3), testMetrics.AltDAFailureCount)

	// Transition to Retrying
	bs.altDALastFailureTime = time.Now().Add(-10 * time.Minute)
	bs.transitionToRetrying()
	require.Equal(t, int(AltDARetrying), testMetrics.AltDAState)

	// Record success
	bs.recordAltDASuccess()
	require.Equal(t, int(AltDAGood), testMetrics.AltDAState)
	require.Equal(t, uint64(0), testMetrics.AltDAFailureCount)
}

// TestShouldRetryAltDA verifies the retry condition logic
func TestShouldRetryAltDA(t *testing.T) {
	t.Run("Returns false when not Degraded", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 3, 5*time.Minute)
		bs.altDAState = AltDAGood
		require.False(t, bs.shouldRetryAltDA())

		bs.altDAState = AltDARetrying
		require.False(t, bs.shouldRetryAltDA())
	})

	t.Run("Returns false when failure time is zero", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 3, 5*time.Minute)
		bs.altDAState = AltDADegraded
		bs.altDALastFailureTime = time.Time{} // Zero time
		require.False(t, bs.shouldRetryAltDA())
	})

	t.Run("Returns false when interval not elapsed", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 3, time.Hour)
		bs.altDAState = AltDADegraded
		bs.altDALastFailureTime = time.Now()
		require.False(t, bs.shouldRetryAltDA())
	})

	t.Run("Returns true when Degraded and interval elapsed", func(t *testing.T) {
		bs, _ := setupWithAltDA(t, 3, 50*time.Millisecond)
		bs.altDAState = AltDADegraded
		bs.altDALastFailureTime = time.Now()

		time.Sleep(60 * time.Millisecond)
		require.True(t, bs.shouldRetryAltDA())
	})
}

// TestTransitionToRetrying verifies the transition logic
func TestTransitionToRetrying(t *testing.T) {
	t.Run("Only transitions from Degraded", func(t *testing.T) {
		bs, testMetrics := setupWithAltDA(t, 3, 5*time.Minute)

		// From Good: no transition
		bs.altDAState = AltDAGood
		bs.transitionToRetrying()
		require.Equal(t, AltDAGood, bs.altDAState)

		// From Retrying: no transition
		bs.altDAState = AltDARetrying
		bs.transitionToRetrying()
		require.Equal(t, AltDARetrying, bs.altDAState)

		// From Degraded: transitions
		bs.altDAState = AltDADegraded
		bs.altDALastFailureTime = time.Now()
		bs.transitionToRetrying()
		require.Equal(t, AltDARetrying, bs.altDAState)
		require.Equal(t, int(AltDARetrying), testMetrics.AltDAState)
	})
}
