package cpu

import (
	"testing"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/stretchr/testify/require"

	"github.com/influxdata/telegraf/plugins/common/psutil"
	"github.com/influxdata/telegraf/testutil"
)

func newCPUStats(ps psutil.PS) *CPU {
	return &CPU{
		ps:             ps,
		CollectCPUTime: true,
		ReportActive:   true,
	}
}

func TestCPUStats(t *testing.T) {
	var mps psutil.MockPS
	defer mps.AssertExpectations(t)
	var acc testutil.Accumulator

	cts := cpu.TimesStat{
		CPU:       "cpu0",
		User:      8.8,
		System:    8.2,
		Idle:      80.1,
		Nice:      1.3,
		Iowait:    0.8389,
		Irq:       0.6,
		Softirq:   0.11,
		Steal:     0.0511,
		Guest:     3.1,
		GuestNice: 0.324,
	}

	cts2 := cpu.TimesStat{
		CPU:       "cpu0",
		User:      24.9,     // increased by 16.1
		System:    10.9,     // increased by 2.7
		Idle:      157.9798, // increased by 77.8798 (for total increase of 100)
		Nice:      3.5,      // increased by 2.2
		Iowait:    0.929,    // increased by 0.0901
		Irq:       1.2,      // increased by 0.6
		Softirq:   0.31,     // increased by 0.2
		Steal:     0.2812,   // increased by 0.2301
		Guest:     11.4,     // increased by 8.3
		GuestNice: 2.524,    // increased by 2.2
	}

	mps.On("CPUTimes").Return([]cpu.TimesStat{cts}, nil)

	cs := newCPUStats(&mps)

	err := cs.Gather(&acc)
	require.NoError(t, err)

	// Computed values are checked with delta > 0 because of floating point arithmetic
	// imprecision
	assertContainsTaggedFloat(t, &acc, "time_user", 8.8, 0)
	assertContainsTaggedFloat(t, &acc, "time_system", 8.2, 0)
	assertContainsTaggedFloat(t, &acc, "time_idle", 80.1, 0)
	assertContainsTaggedFloat(t, &acc, "time_active", 19.9, 0.0005)
	assertContainsTaggedFloat(t, &acc, "time_nice", 1.3, 0)
	assertContainsTaggedFloat(t, &acc, "time_iowait", 0.8389, 0)
	assertContainsTaggedFloat(t, &acc, "time_irq", 0.6, 0)
	assertContainsTaggedFloat(t, &acc, "time_softirq", 0.11, 0)
	assertContainsTaggedFloat(t, &acc, "time_steal", 0.0511, 0)
	assertContainsTaggedFloat(t, &acc, "time_guest", 3.1, 0)
	assertContainsTaggedFloat(t, &acc, "time_guest_nice", 0.324, 0)

	mps2 := psutil.MockPS{}
	mps2.On("CPUTimes").Return([]cpu.TimesStat{cts2}, nil)
	cs.ps = &mps2

	// Should have added cpu percentages too
	err = cs.Gather(&acc)
	require.NoError(t, err)

	assertContainsTaggedFloat(t, &acc, "time_user", 24.9, 0)
	assertContainsTaggedFloat(t, &acc, "time_system", 10.9, 0)
	assertContainsTaggedFloat(t, &acc, "time_idle", 157.9798, 0)
	assertContainsTaggedFloat(t, &acc, "time_active", 42.0202, 0.0005)
	assertContainsTaggedFloat(t, &acc, "time_nice", 3.5, 0)
	assertContainsTaggedFloat(t, &acc, "time_iowait", 0.929, 0)
	assertContainsTaggedFloat(t, &acc, "time_irq", 1.2, 0)
	assertContainsTaggedFloat(t, &acc, "time_softirq", 0.31, 0)
	assertContainsTaggedFloat(t, &acc, "time_steal", 0.2812, 0)
	assertContainsTaggedFloat(t, &acc, "time_guest", 11.4, 0)
	assertContainsTaggedFloat(t, &acc, "time_guest_nice", 2.524, 0)

	assertContainsTaggedFloat(t, &acc, "usage_user", 7.8, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_system", 2.7, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_idle", 77.8798, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_active", 22.1202, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_nice", 0, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_iowait", 0.0901, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_irq", 0.6, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_softirq", 0.2, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_steal", 0.2301, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_guest", 8.3, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_guest_nice", 2.2, 0.0005)
}

// Asserts that a given accumulator contains a measurement of type float64 with
// specific tags within a certain distance of a given expected value. Asserts a failure
// if the measurement is of the wrong type, or if no matching measurements are found
//
// Parameters:
//
//	t *testing.T            : Testing object to use
//	acc testutil.Accumulator: Accumulator to examine
//	field string            : Name of field to examine
//	expectedValue float64   : Value to search for within the measurement
//	delta float64           : Maximum acceptable distance of an accumulated value
//	                          from the expectedValue parameter. Useful when
//	                          floating-point arithmetic imprecision makes looking
//	                          for an exact match impractical
func assertContainsTaggedFloat(
	t *testing.T,
	acc *testutil.Accumulator,
	field string,
	expectedValue, delta float64,
) {
	var actualValue float64
	measurement := "cpu" // always cpu
	for _, pt := range acc.Metrics {
		if pt.Measurement == measurement {
			for fieldname, value := range pt.Fields {
				if fieldname == field {
					if value, ok := value.(float64); ok {
						actualValue = value
						if (value >= expectedValue-delta) && (value <= expectedValue+delta) {
							// Found the point, return without failing
							return
						}
					} else {
						require.Failf(t, "Wrong type", "Measurement %q does not have type float64", measurement)
					}
				}
			}
		}
	}
	require.Failf(t, "Measurement not found",
		"Could not find measurement %q with requested tags within %f of %f, Actual: %f", measurement, delta, expectedValue, actualValue)
}

// TestCPUCountChange tests that no errors are encountered if the number of
// CPUs increases as reported with LXC.
func TestCPUCountIncrease(t *testing.T) {
	var mps psutil.MockPS
	var mps2 psutil.MockPS
	var acc testutil.Accumulator
	var err error

	cs := newCPUStats(&mps)

	mps.On("CPUTimes").Return(
		[]cpu.TimesStat{
			{
				CPU: "cpu0",
			},
		}, nil)

	err = cs.Gather(&acc)
	require.NoError(t, err)

	mps2.On("CPUTimes").Return(
		[]cpu.TimesStat{
			{
				CPU: "cpu0",
			},
			{
				CPU: "cpu1",
			},
		}, nil)
	cs.ps = &mps2

	err = cs.Gather(&acc)
	require.NoError(t, err)
}

// TestCPUTimesDecrease tests that telegraf continue to works after
// CPU times decrease, which seems to occur when Linux system is suspended.
func TestCPUTimesDecrease(t *testing.T) {
	var mps psutil.MockPS
	defer mps.AssertExpectations(t)
	var acc testutil.Accumulator

	cts := cpu.TimesStat{
		CPU:    "cpu0",
		User:   18,
		Idle:   80,
		Iowait: 2,
	}

	cts2 := cpu.TimesStat{
		CPU:    "cpu0",
		User:   38, // increased by 20
		Idle:   40, // decreased by 40
		Iowait: 1,  // decreased by 1
	}

	cts3 := cpu.TimesStat{
		CPU:    "cpu0",
		User:   56,  // increased by 18
		Idle:   120, // increased by 80
		Iowait: 3,   // increased by 2
	}

	mps.On("CPUTimes").Return([]cpu.TimesStat{cts}, nil)

	cs := newCPUStats(&mps)

	err := cs.Gather(&acc)
	require.NoError(t, err)

	// Computed values are checked with delta > 0 because of floating point arithmetic
	// imprecision
	assertContainsTaggedFloat(t, &acc, "time_user", 18, 0)
	assertContainsTaggedFloat(t, &acc, "time_idle", 80, 0)
	assertContainsTaggedFloat(t, &acc, "time_iowait", 2, 0)

	mps2 := psutil.MockPS{}
	mps2.On("CPUTimes").Return([]cpu.TimesStat{cts2}, nil)
	cs.ps = &mps2

	// CPU times decreased. An error should be raised
	err = cs.Gather(&acc)
	require.Error(t, err)

	mps3 := psutil.MockPS{}
	mps3.On("CPUTimes").Return([]cpu.TimesStat{cts3}, nil)
	cs.ps = &mps3

	err = cs.Gather(&acc)
	require.NoError(t, err)

	assertContainsTaggedFloat(t, &acc, "time_user", 56, 0)
	assertContainsTaggedFloat(t, &acc, "time_idle", 120, 0)
	assertContainsTaggedFloat(t, &acc, "time_iowait", 3, 0)

	assertContainsTaggedFloat(t, &acc, "usage_user", 18, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_idle", 80, 0.0005)
	assertContainsTaggedFloat(t, &acc, "usage_iowait", 2, 0.0005)
}

func getFirstCPUFieldFloat(t *testing.T, acc *testutil.Accumulator, field string) float64 {
	t.Helper()
	for _, pt := range acc.Metrics {
		if pt.Measurement != "cpu" {
			continue
		}
		if value, ok := pt.Fields[field]; ok {
			f, ok := value.(float64)
			require.Truef(t, ok, "field %q is not float64 (got %T)", field, value)
			return f
		}
	}
	require.Failf(t, "Field not found", "could not find field %q in cpu metrics", field)
	return 0
}

func TestCPUPercentagesAreClamped(t *testing.T) {
	// Reproduce a case where some counters go backwards but the total still increases
	// (e.g., counter resets/wrap or accounting oddities). We should never emit negative
	// or >100 values for cpu usage percentages.
	var acc testutil.Accumulator

	cts := cpu.TimesStat{
		CPU:  "cpu-total",
		User: 10,
		Idle: 100,
	}
	cts2 := cpu.TimesStat{
		CPU:  "cpu-total",
		User: 120, // +110
		Idle: 90,  // -10 -> would make active > 100% without clamping
	}

	var mps1 psutil.MockPS
	mps1.On("CPUTimes").Return([]cpu.TimesStat{cts}, nil)
	cs := newCPUStats(&mps1)
	cs.ClampPercent = true

	require.NoError(t, cs.Gather(&acc))

	var mps2 psutil.MockPS
	mps2.On("CPUTimes").Return([]cpu.TimesStat{cts2}, nil)
	cs.ps = &mps2

	require.NoError(t, cs.Gather(&acc))

	for _, field := range []string{"usage_user", "usage_idle", "usage_active"} {
		v := getFirstCPUFieldFloat(t, &acc, field)
		require.GreaterOrEqualf(t, v, 0.0, "%s should be >= 0, got %f", field, v)
		require.LessOrEqualf(t, v, 100.0, "%s should be <= 100, got %f", field, v)
	}
}

func TestCPUUserGuestAdjustmentDoesNotGoNegative(t *testing.T) {
	// If guest accounting temporarily exceeds user delta, the adjusted user usage
	// would go negative. Ensure we clamp it to a sane range.
	var acc testutil.Accumulator

	cts := cpu.TimesStat{
		CPU:   "cpu-total",
		User:  10,
		Guest: 1,
		Idle:  100,
	}
	cts2 := cpu.TimesStat{
		CPU:   "cpu-total",
		User:  12, // +2
		Guest: 6,  // +5 -> adjusted user delta would be -3 without clamping
		Idle:  108,
	}

	var mps1 psutil.MockPS
	mps1.On("CPUTimes").Return([]cpu.TimesStat{cts}, nil)
	cs := newCPUStats(&mps1)
	cs.ClampPercent = true
	require.NoError(t, cs.Gather(&acc))

	var mps2 psutil.MockPS
	mps2.On("CPUTimes").Return([]cpu.TimesStat{cts2}, nil)
	cs.ps = &mps2
	require.NoError(t, cs.Gather(&acc))

	usageUser := getFirstCPUFieldFloat(t, &acc, "usage_user")
	require.GreaterOrEqual(t, usageUser, 0.0)
	require.LessOrEqual(t, usageUser, 100.0)
}

func TestCPUInvalidPercentBehaviorSkip(t *testing.T) {
	// When configured, invalid percentage calculations should not error and should
	// skip usage emission.
	var acc testutil.Accumulator

	cts := cpu.TimesStat{
		CPU:  "cpu0",
		User: 10,
		Idle: 100,
	}
	cts2 := cpu.TimesStat{
		CPU:  "cpu0",
		User: 120,
		Idle: 90, // negative idle delta can yield >100% user if not clamped/validated
	}

	var mps1 psutil.MockPS
	mps1.On("CPUTimes").Return([]cpu.TimesStat{cts}, nil)
	cs := newCPUStats(&mps1)
	cs.PercentInvalidBehavior = "skip"

	require.NoError(t, cs.Gather(&acc))

	var mps2 psutil.MockPS
	mps2.On("CPUTimes").Return([]cpu.TimesStat{cts2}, nil)
	cs.ps = &mps2

	require.NoError(t, cs.Gather(&acc))

	// There should be no usage_user field emitted for the invalid interval.
	found := false
	for _, pt := range acc.Metrics {
		if pt.Measurement != "cpu" {
			continue
		}
		if _, ok := pt.Fields["usage_user"]; ok {
			found = true
			break
		}
	}
	require.False(t, found, "expected no usage_* fields emitted when skipping invalid percent calculations")
}

func TestCPUInvalidPercentBehaviorLast(t *testing.T) {
	// When configured, invalid percentage calculations should emit the last
	// known-good usage snapshot.
	var acc testutil.Accumulator

	cts := cpu.TimesStat{
		CPU:  "cpu0",
		User: 10,
		Idle: 100,
	}
	cts2 := cpu.TimesStat{
		CPU:  "cpu0",
		User: 20,  // +10
		Idle: 190, // +90 -> totalDelta 100 => usage_user 10, usage_idle 90
	}
	cts3 := cpu.TimesStat{
		CPU:  "cpu0",
		User: 140, // +120
		Idle: 180, // -10 -> invalid interval
	}

	var mps1 psutil.MockPS
	mps1.On("CPUTimes").Return([]cpu.TimesStat{cts}, nil)
	cs := newCPUStats(&mps1)
	cs.PercentInvalidBehavior = "last"

	require.NoError(t, cs.Gather(&acc))

	var mps2 psutil.MockPS
	mps2.On("CPUTimes").Return([]cpu.TimesStat{cts2}, nil)
	cs.ps = &mps2
	require.NoError(t, cs.Gather(&acc))

	// Capture last-good value
	lastGood := getFirstCPUFieldFloat(t, &acc, "usage_user")

	var mps3 psutil.MockPS
	mps3.On("CPUTimes").Return([]cpu.TimesStat{cts3}, nil)
	cs.ps = &mps3
	require.NoError(t, cs.Gather(&acc))

	// The emitted value should remain the last-good one.
	v := getFirstCPUFieldFloat(t, &acc, "usage_user")
	require.InDelta(t, lastGood, v, 0.0005)
}
