package rmq

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/streadway/amqp"
	"github.com/stretchr/testify/assert"
)

// getTestConfig возвращает конфигурацию для тестов из переменных окружения
func getTestConfig() RMQConfig {
	// Значения по умолчанию
	host := "localhost"
	login := "guest"
	password := "guest"
	port := "5672"
	exchange := "test_exchange"
	queue := "test_queue"

	// Получаем значения из переменных окружения, если они установлены
	if envHost := os.Getenv("RMQ_HOST"); envHost != "" {
		host = envHost
	}
	if envUser := os.Getenv("RMQ_USER"); envUser != "" {
		login = envUser
	}
	if envPass := os.Getenv("RMQ_PASSWORD"); envPass != "" {
		password = envPass
	}
	if envPort := os.Getenv("RMQ_PORT"); envPort != "" {
		port = envPort
	}
	if envExchange := os.Getenv("RMQ_EXCHANGE"); envExchange != "" {
		exchange = envExchange
	}
	if envQueue := os.Getenv("RMQ_QUEUE"); envQueue != "" {
		queue = envQueue
	}

	// Формируем и возвращаем конфигурацию
	return RMQConfig{
		Host:             fmt.Sprintf("%s:%s", host, port),
		Login:            login,
		Password:         password,
		Exchange:         exchange,
		Queue:            queue,
		PrefetchCount:    1,
		ReconnectDelay:   time.Second * 2,
		ResponseTimeout:  time.Second * 5,
		MaxRetryAttempts: 3,
		RetryDelay:       time.Second * 1,
		Routes: map[string]func([]byte) ([]byte, error){
			"test.route": func(body []byte) ([]byte, error) {
				return body, nil // Эхо-обработчик для тестов
			},
		},
	}
}

// TestRMQIntegration проверяет полный цикл подключения, отправки и получения сообщений
func TestRMQIntegration(t *testing.T) {
	// Пропускаем интеграционный тест, если не установлен соответствующий флаг
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Получаем конфигурацию для тестов
	config := getTestConfig()

	// Создаем сервис
	service := NewRMQService()

	// Подключаемся к RabbitMQ
	connected := service.Connect(config)
	defer service.Disconnect()

	// Проверяем успешность подключения
	if !connected {
		t.Fatalf("Failed to connect to RabbitMQ. Host: %s", config.Host)
	}

	// Запускаем слушателя в отдельной горутине
	go service.Listen()

	// Даем время на инициализацию слушателя
	time.Sleep(time.Second)

	// Тестируем отправку события без ожидания ответа
	err := service.PublishEvent("test.event", map[string]string{"message": "test event"})
	assert.NoError(t, err, "PublishEvent should not return error")

	// Тестируем отправку сообщения с ожиданием ответа
	testMsg := map[string]string{"message": "test message with response"}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	var response map[string]string
	err = service.SendWithResponse(ctx, "test.route", testMsg, &response)

	assert.NoError(t, err, "SendWithResponse should not return error")
	assert.Equal(t, testMsg["message"], response["message"], "Response should match sent message")
}

// TestCreateAndRemoveExchangeQueue создает временные exchange и queue для тестов и удаляет их после
func TestCreateAndRemoveExchangeQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Получаем конфигурацию для тестов
	config := getTestConfig()

	// Создаем уникальные имена для тестов, если они не установлены через переменные окружения
	if os.Getenv("RMQ_EXCHANGE") == "" {
		config.Exchange = fmt.Sprintf("test_exchange_%d", time.Now().UnixNano())
	}
	if os.Getenv("RMQ_QUEUE") == "" {
		config.Queue = fmt.Sprintf("test_queue_%d", time.Now().UnixNano())
	}

	// Подключаемся напрямую через amqp для настройки и очистки
	connString := fmt.Sprintf("amqp://%s:%s@%s", config.Login, config.Password, config.Host)
	conn, err := amqp.Dial(connString)
	if err != nil {
		t.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("Failed to open channel: %v", err)
	}
	defer ch.Close()

	// Создаем exchange
	err = ch.ExchangeDeclare(
		config.Exchange, // имя
		"topic",         // тип
		true,            // durable
		false,           // auto-deleted
		false,           // internal
		false,           // no-wait
		nil,             // аргументы
	)
	if err != nil {
		t.Fatalf("Failed to declare exchange: %v", err)
	}

	// Создаем queue
	_, err = ch.QueueDeclare(
		config.Queue, // имя
		true,         // durable
		false,        // auto-deleted
		false,        // exclusive
		false,        // no-wait
		nil,          // аргументы
	)
	if err != nil {
		t.Fatalf("Failed to declare queue: %v", err)
	}

	// Привязываем queue к exchange
	err = ch.QueueBind(
		config.Queue,    // имя очереди
		"test.#",        // ключ маршрутизации
		config.Exchange, // имя обмена
		false,           // no-wait
		nil,             // аргументы
	)
	if err != nil {
		t.Fatalf("Failed to bind queue: %v", err)
	}

	// Здесь можно запустить тесты с нашей конфигурацией
	t.Logf("Created exchange %s and queue %s for testing", config.Exchange, config.Queue)

	// После тестов удаляем созданные ресурсы
	// Исправлено: QueueDelete возвращает два значения
	purgedCount, err := ch.QueueDelete(
		config.Queue, // имя
		false,        // ifUnused
		false,        // ifEmpty
		false,        // noWait
	)
	if err != nil {
		t.Logf("Warning: Failed to delete queue: %v", err)
	} else {
		t.Logf("Queue deleted, purged %d messages", purgedCount)
	}

	err = ch.ExchangeDelete(
		config.Exchange, // имя
		false,           // ifUnused
		false,           // noWait
	)
	if err != nil {
		t.Logf("Warning: Failed to delete exchange: %v", err)
	} else {
		t.Logf("Exchange deleted successfully")
	}

	t.Logf("Cleaned up exchange and queue")
}
