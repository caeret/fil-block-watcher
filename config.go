package filblockwatcher

import (
	"time"

	"github.com/caeret/logging"
)

var defaultCfg = config{
	logger:           logging.Default(),
	bootstrapTimeout: time.Second * 5,
	listenPort:       0,
}

type config struct {
	logger            logging.Logger
	bootstrapTimeout  time.Duration
	listenPort        int
	BlockEventHandler BlockEventHandler
}

type Option func(cfg *config)

func WithLogger(logger logging.Logger) Option {
	return func(cfg *config) {
		cfg.logger = logger
	}
}

func BootstrapTimeout(duration time.Duration) Option {
	return func(cfg *config) {
		cfg.bootstrapTimeout = duration
	}
}

func ListenPort(port int) Option {
	return func(cfg *config) {
		cfg.listenPort = port
	}
}

func WithBlockEventHandler(handler BlockEventHandler) Option {
	return func(cfg *config) {
		cfg.BlockEventHandler = handler
	}
}
