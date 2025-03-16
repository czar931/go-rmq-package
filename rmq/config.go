package rmq

import (
	"time"
)

// RMQConfig содержит настройки для RabbitMQ
type RMQConfig struct {
	// Основные настройки соединения
	Host          string
	Login         string
	Password      string
	Exchange      string
	Queue         string
	Routes        map[string]func([]byte) ([]byte, error)
	PrefetchCount int

	// Настройки для ретраев и таймаутов
	ReconnectDelay   time.Duration // Задержка между попытками повторного подключения
	ResponseTimeout  time.Duration // Таймаут для ожидания ответа
	MaxRetryAttempts int           // Максимальное количество попыток отправки сообщения
	RetryDelay       time.Duration // Задержка между повторными попытками отправки
}

// Response структура для обработки ответа
type Response struct {
	Body    []byte
	Headers map[string]interface{}
	Error   error
}

// Значения по умолчанию для конфигурации
var DefaultConfig = RMQConfig{
	PrefetchCount:    1,
	ReconnectDelay:   time.Second * 5,
	ResponseTimeout:  time.Second * 30,
	MaxRetryAttempts: 3,
	RetryDelay:       time.Second * 2,
}

// applyDefaultConfig применяет настройки по умолчанию, если они не указаны
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

// NewDefaultConfig возвращает конфигурацию со значениями по умолчанию,
// которую можно затем модифицировать
func NewDefaultConfig() RMQConfig {
	return DefaultConfig
}

// ConnectionString возвращает строку подключения для RabbitMQ
func (c *RMQConfig) ConnectionString() string {
	return "amqp://" + c.Login + ":" + c.Password + "@" + c.Host + ":5672"
}

// WithRoutes добавляет маршруты в конфигурацию и возвращает ее
func (c RMQConfig) WithRoutes(routes map[string]func([]byte) ([]byte, error)) RMQConfig {
	c.Routes = routes
	return c
}

// WithQueue устанавливает имя очереди и возвращает конфигурацию
func (c RMQConfig) WithQueue(queue string) RMQConfig {
	c.Queue = queue
	return c
}

// WithExchange устанавливает имя обмена и возвращает конфигурацию
func (c RMQConfig) WithExchange(exchange string) RMQConfig {
	c.Exchange = exchange
	return c
}

// WithPrefetchCount устанавливает prefetch count и возвращает конфигурацию
func (c RMQConfig) WithPrefetchCount(count int) RMQConfig {
	c.PrefetchCount = count
	return c
}

// WithRetrySettings устанавливает настройки ретраев и возвращает конфигурацию
func (c RMQConfig) WithRetrySettings(maxAttempts int, retryDelay time.Duration) RMQConfig {
	c.MaxRetryAttempts = maxAttempts
	c.RetryDelay = retryDelay
	return c
}

// WithTimeouts устанавливает таймауты и возвращает конфигурацию
func (c RMQConfig) WithTimeouts(responseTimeout, reconnectDelay time.Duration) RMQConfig {
	c.ResponseTimeout = responseTimeout
	c.ReconnectDelay = reconnectDelay
	return c
}
