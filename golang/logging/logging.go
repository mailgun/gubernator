package logging

import (
	"encoding/json"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type LogLevelJSON struct {
	Level logrus.Level
}

func (ll LogLevelJSON) MarshalJSON() ([]byte, error) {
	return json.Marshal(ll.String())
}

func (ll *LogLevelJSON) UnmarshalJSON(b []byte) error {
	var v interface{}
	var err error

	if err = json.Unmarshal(b, &v); err != nil {
		return err
	}

	switch value := v.(type) {
	case float64:
		ll.Level = logrus.Level(int32(value))
	case string:
		ll.Level, err = logrus.ParseLevel(value)
	default:
		return errors.New("invalid log level")
	}
	return err
}

func (ll LogLevelJSON) String() string {
	return ll.Level.String()
}
