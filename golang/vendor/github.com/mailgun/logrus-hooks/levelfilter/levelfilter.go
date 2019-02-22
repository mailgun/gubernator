package levelfilter

import "github.com/sirupsen/logrus"

type LevelFilter struct {
	hook   logrus.Hook
	levels []logrus.Level
}

func New(hook logrus.Hook, level logrus.Level) *LevelFilter {
	levels := make([]logrus.Level, 0, len(logrus.AllLevels))
	for _, l := range hook.Levels() {
		if l <= level {
			levels = append(levels, l)
		}
	}

	return &LevelFilter{
		hook:   hook,
		levels: levels,
	}
}

func (lf *LevelFilter) Levels() []logrus.Level {
	return lf.levels
}

func (lf *LevelFilter) Fire(entry *logrus.Entry) error {
	return lf.hook.Fire(entry)
}
