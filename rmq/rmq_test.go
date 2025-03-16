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

func getTestConfig() RMQConfig {
	host := "localhost"
	login := "guest"
	password := "guest"
	port := "5672"
	exchange := "test_exchange"
	queue := "test_queue"

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

	return RMQConfig{
		Host:             host,
		Port:             port,
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
				return body, nil
			},
		},
	}
}

func TestRMQIntegration(t *testing.T) {

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := getTestConfig()

	service := NewRMQService()

	connected := service.Connect(config)
	defer service.Disconnect()

	if !connected {
		t.Fatalf("Failed to connect to RabbitMQ. Host: %s", config.Host)
	}

	go service.Listen()

	time.Sleep(time.Second)

	err := service.PublishEvent("test.event", map[string]string{"message": "test event"})
	assert.NoError(t, err, "PublishEvent should not return error")

	testMsg := map[string]string{"message": "test message with response"}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	var response map[string]string
	err = service.SendWithResponse(ctx, "test.route", testMsg, &response)

	assert.NoError(t, err, "SendWithResponse should not return error")
	assert.Equal(t, testMsg["message"], response["message"], "Response should match sent message")
}

func TestCreateAndRemoveExchangeQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	config := getTestConfig()

	if os.Getenv("RMQ_EXCHANGE") == "" {
		config.Exchange = fmt.Sprintf("test_exchange_%d", time.Now().UnixNano())
	}
	if os.Getenv("RMQ_QUEUE") == "" {
		config.Queue = fmt.Sprintf("test_queue_%d", time.Now().UnixNano())
	}

	connString := fmt.Sprintf("amqp://%s:%s@%s:%s", config.Login, config.Password, config.Host, config.Port)
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

	err = ch.ExchangeDeclare(
		config.Exchange,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to declare exchange: %v", err)
	}

	_, err = ch.QueueDeclare(
		config.Queue,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to declare queue: %v", err)
	}

	err = ch.QueueBind(
		config.Queue,
		"test.#",
		config.Exchange,
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to bind queue: %v", err)
	}

	t.Logf("Created exchange %s and queue %s for testing", config.Exchange, config.Queue)

	purgedCount, err := ch.QueueDelete(
		config.Queue,
		false,
		false,
		false,
	)
	if err != nil {
		t.Logf("Warning: Failed to delete queue: %v", err)
	} else {
		t.Logf("Queue deleted, purged %d messages", purgedCount)
	}

	err = ch.ExchangeDelete(
		config.Exchange,
		false,
		false,
	)
	if err != nil {
		t.Logf("Warning: Failed to delete exchange: %v", err)
	} else {
		t.Logf("Exchange deleted successfully")
	}

	t.Logf("Cleaned up exchange and queue")
}
