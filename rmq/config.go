package rmq

import (
	"time"
)

type RMQConfig struct {
	Host          string
	Port          string
	Login         string
	Password      string
	Exchange      string
	Queue         string
	Routes        map[string]func([]byte) ([]byte, error)
	PrefetchCount int

	ReconnectDelay   time.Duration // Delay between reconnection attempts
	ResponseTimeout  time.Duration // Timeout for waiting for a response
	MaxRetryAttempts int           // Maximum number of attempts to send a message
	RetryDelay       time.Duration // Delay between retries to send
}

type Response struct {
	Body    []byte
	Headers map[string]interface{}
	Error   error
}

var DefaultConfig = RMQConfig{
	Port:             "5672",
	PrefetchCount:    1,
	ReconnectDelay:   time.Second * 5,
	ResponseTimeout:  time.Second * 30,
	MaxRetryAttempts: 3,
	RetryDelay:       time.Second * 2,
}

// Applies default settings if they are not specified
func applyDefaultConfig(config RMQConfig) RMQConfig {
	if config.PrefetchCount == 0 {
		config.PrefetchCount = DefaultConfig.PrefetchCount
	}

	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = DefaultConfig.ReconnectDelay
	}

	if config.ResponseTimeout == 0 {
		config.ResponseTimeout = DefaultConfig.ResponseTimeout
	}

	if config.MaxRetryAttempts == 0 {
		config.MaxRetryAttempts = DefaultConfig.MaxRetryAttempts
	}

	if config.RetryDelay == 0 {
		config.RetryDelay = DefaultConfig.RetryDelay
	}

	return config
}

func NewDefaultConfig() RMQConfig {
	return DefaultConfig
}

func (c *RMQConfig) ConnectionString() string {
	return "amqp://" + c.Login + ":" + c.Password + "@" + c.Host + ":" + c.Port
}

func (c RMQConfig) WithRoutes(routes map[string]func([]byte) ([]byte, error)) RMQConfig {
	c.Routes = routes
	return c
}

func (c RMQConfig) WithQueue(queue string) RMQConfig {
	c.Queue = queue
	return c
}

func (c RMQConfig) WithExchange(exchange string) RMQConfig {
	c.Exchange = exchange
	return c
}

func (c RMQConfig) WithPrefetchCount(count int) RMQConfig {
	c.PrefetchCount = count
	return c
}

func (c RMQConfig) WithRetrySettings(maxAttempts int, retryDelay time.Duration) RMQConfig {
	c.MaxRetryAttempts = maxAttempts
	c.RetryDelay = retryDelay
	return c
}

func (c RMQConfig) WithTimeouts(responseTimeout, reconnectDelay time.Duration) RMQConfig {
	c.ResponseTimeout = responseTimeout
	c.ReconnectDelay = reconnectDelay
	return c
}
