package logging

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"log/syslog"

	"github.com/mailgun/holster"
	"github.com/mailgun/logrus-hooks/kafkahook"
	"github.com/mailgun/logrus-hooks/levelfilter"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	logrus_syslog "github.com/sirupsen/logrus/hooks/syslog"
)

var log = logrus.WithField("category", "service")

type Config struct {
	NoStdout bool                  `json:"no_stdout"`
	LogLevel LogLevelJSON          `json:"log_level"`
	Handlers map[string]HandlerCfg `json:"handlers"`
}

type HandlerCfg struct {
	LogLevel LogLevelJSON `json:"log_level"`
	Topic    string       `json:"topic"`
	Nodes    []string     `json:"nodes"`
}

func Log() *logrus.Entry {
	return log
}

func AddFields(fields logrus.Fields) {
	log = log.WithFields(fields)
}

func Init(ctx context.Context, cfg Config, appName string) error {
	for name, handler := range cfg.Handlers {
		holster.SetDefault(&handler.LogLevel.Level, logrus.DebugLevel)
		switch name {
		case "syslog":
			h, err := logrus_syslog.NewSyslogHook(
				"udp", "127.0.0.1:514", syslog.LOG_INFO|syslog.LOG_MAIL, appName)
			if err != nil {
				continue
			}
			logrus.AddHook(levelfilter.New(h, handler.LogLevel.Level))
		case "kafka":
			h, err := kafkahook.NewWithContext(ctx, kafkahook.Config{
				Endpoints: handler.Nodes,
				Topic:     handler.Topic,
			})
			if err != nil {
				continue
			}
			logrus.AddHook(levelfilter.New(h, handler.LogLevel.Level))
		}
	}
	if cfg.NoStdout {
		logrus.SetOutput(ioutil.Discard)
	}
	holster.SetDefault(&cfg.LogLevel.Level, logrus.DebugLevel)
	logrus.SetLevel(cfg.LogLevel.Level)
	return nil
}

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
