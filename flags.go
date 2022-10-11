/*
Copyright 2018-2022 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gubernator

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

func getEnvMetricFlags(log FieldLogger, name string) MetricFlags {
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
