//go:generate ../../../tools/readme_config_includer/generator
package cpu

import (
	_ "embed"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/common/psutil"
	"github.com/influxdata/telegraf/plugins/inputs"
)

//go:embed sample.conf
var sampleConfig string

type CPU struct {
	ps         psutil.PS
	lastStats  map[string]cpu.TimesStat
	lastUsage  map[string]map[string]float64
	cpuInfo    map[string]cpu.InfoStat
	coreID     bool
	physicalID bool

	PerCPU         bool `toml:"percpu"`
	TotalCPU       bool `toml:"totalcpu"`
	CollectCPUTime bool `toml:"collect_cpu_time"`
	ReportActive   bool `toml:"report_active"`
	CoreTags       bool `toml:"core_tags"`
	ClampPercent   bool `toml:"clamp_percentages"`
	// PercentInvalidBehavior controls how to handle invalid percentage calculations.
	// Valid options:
	// - "error" (default): return an error (current behavior)
	// - "skip": skip emitting usage_* fields for the interval
	// - "last": emit the last known-good usage_* values for that CPU
	PercentInvalidBehavior string `toml:"percent_invalid_behavior"`

	Log telegraf.Logger `toml:"-"`
}

func (c *CPU) percentInvalidBehavior() string {
	behavior := strings.ToLower(strings.TrimSpace(c.PercentInvalidBehavior))
	if behavior == "" {
		return "error"
	}
	switch behavior {
	case "error", "skip", "last":
		return behavior
	default:
		if c.Log != nil {
			c.Log.Warnf("Invalid percent_invalid_behavior %q, using %q", c.PercentInvalidBehavior, "error")
		}
		return "error"
	}
}

func usagePercent(delta, totalDelta float64, clamp bool) float64 {
	// Handle invalid input defensively to avoid returning NaN/Inf
	if totalDelta <= 0 || math.IsNaN(totalDelta) || math.IsInf(totalDelta, 0) {
		return 0
	}
	if math.IsNaN(delta) || math.IsInf(delta, 0) {
		return 0
	}

	if clamp {
		// CPU times should be monotonically increasing. In the presence of CPU hotplug,
		// suspend/resume, counter resets/wrap, or odd guest-accounting, deltas can become
		// negative or exceed totalDelta. Clamp to a sane range so we never emit out-of-
		// range percentages.
		if delta < 0 {
			delta = 0
		} else if delta > totalDelta {
			delta = totalDelta
		}
	}

	return 100 * delta / totalDelta
}

const usageEpsilon = 1e-9

func normalizeUsagePercent(v float64) (float64, bool) {
	// returns (normalizedValue, valid)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}

	// Clamp tiny rounding errors to the range.
	if v < 0 {
		if v > -usageEpsilon {
			return 0, true
		}
		return 0, false
	}
	if v > 100 {
		if v < 100+usageEpsilon {
			return 100, true
		}
		return 0, false
	}

	return v, true
}

func snapshotToFields(snapshot map[string]float64) map[string]interface{} {
	fields := make(map[string]interface{}, len(snapshot))
	for k, v := range snapshot {
		fields[k] = v
	}
	return fields
}

func (*CPU) SampleConfig() string {
	return sampleConfig
}

func (c *CPU) Init() error {
	if c.CoreTags {
		cpuInfo, err := cpu.Info()
		if err == nil {
			c.coreID = cpuInfo[0].CoreID != ""
			c.physicalID = cpuInfo[0].PhysicalID != ""

			c.cpuInfo = make(map[string]cpu.InfoStat)
			for _, ci := range cpuInfo {
				c.cpuInfo[fmt.Sprintf("cpu%d", ci.CPU)] = ci
			}
		} else {
			c.Log.Warnf("Failed to gather info about CPUs: %s", err)
		}
	}

	return nil
}

func (c *CPU) Gather(acc telegraf.Accumulator) error {
	times, err := c.ps.CPUTimes(c.PerCPU, c.TotalCPU)
	if err != nil {
		return fmt.Errorf("error getting CPU info: %w", err)
	}
	now := time.Now()
	behavior := c.percentInvalidBehavior()
	if c.lastUsage == nil {
		c.lastUsage = make(map[string]map[string]float64)
	}

	for _, cts := range times {
		tags := map[string]string{
			"cpu": cts.CPU,
		}
		if c.coreID {
			tags["core_id"] = c.cpuInfo[cts.CPU].CoreID
		}
		if c.physicalID {
			tags["physical_id"] = c.cpuInfo[cts.CPU].PhysicalID
		}

		total := totalCPUTime(cts)
		active := activeCPUTime(cts)

		if c.CollectCPUTime {
			// Add cpu time metrics
			fieldsC := map[string]interface{}{
				"time_user":       cts.User,
				"time_system":     cts.System,
				"time_idle":       cts.Idle,
				"time_nice":       cts.Nice,
				"time_iowait":     cts.Iowait,
				"time_irq":        cts.Irq,
				"time_softirq":    cts.Softirq,
				"time_steal":      cts.Steal,
				"time_guest":      cts.Guest,
				"time_guest_nice": cts.GuestNice,
			}
			if c.ReportActive {
				fieldsC["time_active"] = activeCPUTime(cts)
			}
			acc.AddCounter("cpu", fieldsC, tags, now)
		}

		// Add in percentage
		if len(c.lastStats) == 0 {
			// If it's the 1st gather, can't get CPU Usage stats yet
			continue
		}

		lastCts, ok := c.lastStats[cts.CPU]
		if !ok {
			continue
		}
		lastTotal := totalCPUTime(lastCts)
		lastActive := activeCPUTime(lastCts)
		totalDelta := total - lastTotal

		if totalDelta < 0 {
			if behavior == "error" {
				err = errors.New("current total CPU time is less than previous total CPU time")
				break
			}
			if behavior == "last" {
				if snapshot, ok := c.lastUsage[cts.CPU]; ok && len(snapshot) > 0 {
					acc.AddGauge("cpu", snapshotToFields(snapshot), tags, now)
				}
			}
			continue
		}

		if totalDelta == 0 {
			continue
		}

		snapshot := make(map[string]float64, 11)

		if c.ClampPercent {
			snapshot["usage_user"] = usagePercent((cts.User-lastCts.User)-(cts.Guest-lastCts.Guest), totalDelta, true)
			snapshot["usage_system"] = usagePercent(cts.System-lastCts.System, totalDelta, true)
			snapshot["usage_idle"] = usagePercent(cts.Idle-lastCts.Idle, totalDelta, true)
			snapshot["usage_nice"] = usagePercent((cts.Nice-lastCts.Nice)-(cts.GuestNice-lastCts.GuestNice), totalDelta, true)
			snapshot["usage_iowait"] = usagePercent(cts.Iowait-lastCts.Iowait, totalDelta, true)
			snapshot["usage_irq"] = usagePercent(cts.Irq-lastCts.Irq, totalDelta, true)
			snapshot["usage_softirq"] = usagePercent(cts.Softirq-lastCts.Softirq, totalDelta, true)
			snapshot["usage_steal"] = usagePercent(cts.Steal-lastCts.Steal, totalDelta, true)
			snapshot["usage_guest"] = usagePercent(cts.Guest-lastCts.Guest, totalDelta, true)
			snapshot["usage_guest_nice"] = usagePercent(cts.GuestNice-lastCts.GuestNice, totalDelta, true)
			if c.ReportActive {
				snapshot["usage_active"] = usagePercent(active-lastActive, totalDelta, true)
			}
		} else {
			// Raw calculation (backwards-compatible). If configured to handle invalid
			// values, normalize tiny rounding errors and validate bounds.
			raw := map[string]float64{
				"usage_user":       100 * ((cts.User - lastCts.User) - (cts.Guest - lastCts.Guest)) / totalDelta,
				"usage_system":     100 * (cts.System - lastCts.System) / totalDelta,
				"usage_idle":       100 * (cts.Idle - lastCts.Idle) / totalDelta,
				"usage_nice":       100 * ((cts.Nice - lastCts.Nice) - (cts.GuestNice - lastCts.GuestNice)) / totalDelta,
				"usage_iowait":     100 * (cts.Iowait - lastCts.Iowait) / totalDelta,
				"usage_irq":        100 * (cts.Irq - lastCts.Irq) / totalDelta,
				"usage_softirq":    100 * (cts.Softirq - lastCts.Softirq) / totalDelta,
				"usage_steal":      100 * (cts.Steal - lastCts.Steal) / totalDelta,
				"usage_guest":      100 * (cts.Guest - lastCts.Guest) / totalDelta,
				"usage_guest_nice": 100 * (cts.GuestNice - lastCts.GuestNice) / totalDelta,
			}
			if c.ReportActive {
				raw["usage_active"] = 100 * (active - lastActive) / totalDelta
			}

			invalid := false
			for k, v := range raw {
				if behavior == "error" {
					snapshot[k] = v
					continue
				}
				nv, ok := normalizeUsagePercent(v)
				if !ok {
					invalid = true
					break
				}
				snapshot[k] = nv
			}

			if invalid {
				switch behavior {
				case "skip":
					continue
				case "last":
					if last, ok := c.lastUsage[cts.CPU]; ok && len(last) > 0 {
						acc.AddGauge("cpu", snapshotToFields(last), tags, now)
					}
					continue
				default:
					// "error" handled above
				}
			}
		}

		acc.AddGauge("cpu", snapshotToFields(snapshot), tags, now)
		c.lastUsage[cts.CPU] = snapshot
	}

	c.lastStats = make(map[string]cpu.TimesStat)
	for _, cts := range times {
		c.lastStats[cts.CPU] = cts
	}

	return err
}

func totalCPUTime(t cpu.TimesStat) float64 {
	total := t.User + t.System + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal + t.Idle
	return total
}

func activeCPUTime(t cpu.TimesStat) float64 {
	active := totalCPUTime(t) - t.Idle
	return active
}

func init() {
	inputs.Add("cpu", func() telegraf.Input {
		return &CPU{
			PerCPU:   true,
			TotalCPU: true,
			ps:       psutil.NewSystemPS(),
		}
	})
}
