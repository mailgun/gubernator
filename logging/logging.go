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
