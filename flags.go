package gubernator

import "github.com/sirupsen/logrus"

const (
	FlagOSMetrics MetricFlags = 1 << iota
	FlagGolangMetrics
)

type MetricFlags int64

func (f *MetricFlags) Set(flag MetricFlags, set bool) {
	if set {
		*f = *f | flag
	} else {
		mask := *f ^ flag
		*f &= mask
	}
}

func (f *MetricFlags) Has(flag MetricFlags) bool {
	return *f&flag != 0
}

func getEnvMetricFlags(log logrus.FieldLogger, name string) MetricFlags {
	flags := getEnvSlice(name)
	if len(flags) == 0 {
		return 0
	}
	var result MetricFlags

	for _, f := range flags {
		switch f {
		case "os":
			result.Set(FlagOSMetrics, true)
		case "golang":
			result.Set(FlagGolangMetrics, true)
		default:
			log.Errorf("invalid flag '%s' for '%s' valid options are ['os', 'golang']", f, name)
		}
	}
	return result
}
