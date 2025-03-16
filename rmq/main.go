package rmq

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/streadway/amqp"
)

// RMQ интерфейс для работы с RabbitMQ
type RMQ interface {
	Connect(config RMQConfig) bool
	Disconnect()
	Listen()
	// Отправка сообщения без ожидания ответа
	PublishEvent(topic string, message interface{}) error
	// Отправка сообщения с ожиданием ответа
	SendWithResponse(ctx context.Context, topic string, message interface{}, result interface{}) error
}

// RMQService реализация интерфейса RMQ
type RMQService struct {
	conn           *amqp.Connection
	connClosed     chan *amqp.Error
	ch             *amqp.Channel
	chClosed       chan *amqp.Error
	configs        RMQConfig
	replyQueue     amqp.Queue
	exitCh         chan bool
	correlationMap sync.Map
	mu             sync.Mutex
	connected      bool
	reconnecting   bool
}

// NewRMQService создает новый экземпляр RMQService
func NewRMQService() *RMQService {
	return &RMQService{
		exitCh:    make(chan bool),
		connected: false,
	}
}

// Connect устанавливает соединение с RabbitMQ
func (s *RMQService) Connect(config RMQConfig) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	config = applyDefaultConfig(config)
	s.configs = config

	if !s.connectInternal() {
		return false
	}

	go s.monitorConnection()

	return true
}

func (s *RMQService) connectInternal() bool {
	connectionString := fmt.Sprintf("amqp://%s:%s@%s:%s",
		s.configs.Login, s.configs.Password, s.configs.Host, s.configs.Port)

	conn, err := amqp.Dial(connectionString)
	if err != nil {
		log.Printf("Failed to connect to RabbitMQ: %v", err)
		return false
	}

	s.conn = conn
	s.connClosed = make(chan *amqp.Error)
	s.conn.NotifyClose(s.connClosed)

	ch, err := conn.Channel()
	if err != nil {
		log.Printf("Failed to open a channel: %v", err)
		s.conn.Close()
		return false
	}

	s.ch = ch
	s.chClosed = make(chan *amqp.Error)
	s.ch.NotifyClose(s.chClosed)

	err = ch.Qos(
		s.configs.PrefetchCount,
		0,
		false,
	)
	if err != nil {
		log.Printf("Failed to set QoS: %v", err)
		s.ch.Close()
		s.conn.Close()
		return false
	}

	if s.configs.Exchange != "" {
		err = s.ch.ExchangeDeclare(
			s.configs.Exchange,
			"topic",
			true,
			false,
			false,
			false,
			nil,
		)
		if err != nil {
			log.Printf("Failed to declare exchange: %v", err)
			s.ch.Close()
			s.conn.Close()
			return false
		}
	}

	err = s.setupReplyQueue()
	if err != nil {
		log.Printf("Failed to setup reply queue: %v", err)
		s.ch.Close()
		s.conn.Close()
		return false
	}

	if s.configs.Queue != "" {
		_, err = ch.QueueDeclare(
			s.configs.Queue,
			true,
			false,
			false,
			false,
			nil,
		)
		if err != nil {
			log.Printf("Failed to declare queue: %v", err)
			s.ch.Close()
			s.conn.Close()
			return false
		}

		for path := range s.configs.Routes {
			err = ch.QueueBind(s.configs.Queue, path, s.configs.Exchange, false, nil)
			if err != nil {
				log.Printf("Failed to bind queue %s: %v", path, err)
				s.ch.Close()
				s.conn.Close()
				return false
			}
		}
	}

	s.connected = true
	log.Println("Successfully connected to RabbitMQ")
	return true
}

func (s *RMQService) monitorConnection() {
	for {
		select {
		case <-s.exitCh:
			return
		case err := <-s.connClosed:
			if s.reconnecting {
				continue
			}
			s.reconnecting = true

			log.Printf("RabbitMQ connection closed: %v. Reconnecting...", err)
			s.reconnect()

		case err := <-s.chClosed:
			if s.reconnecting {
				continue
			}
			s.reconnecting = true

			log.Printf("RabbitMQ channel closed: %v. Reconnecting...", err)
			s.reconnect()
		}
	}
}

func (s *RMQService) reconnect() {
	s.mu.Lock()
	defer func() {
		s.reconnecting = false
		s.mu.Unlock()
	}()

	if s.ch != nil {
		s.ch.Close()
	}

	if s.conn != nil {
		s.conn.Close()
	}

	for attempt := 1; attempt <= s.configs.MaxRetryAttempts; attempt++ {
		log.Printf("Reconnection attempt %d/%d", attempt, s.configs.MaxRetryAttempts)

		if s.connectInternal() {
			return
		}

		time.Sleep(s.configs.ReconnectDelay)
	}

	log.Printf("Failed to reconnect after %d attempts", s.configs.MaxRetryAttempts)
}

func (s *RMQService) setupReplyQueue() error {
	q, err := s.ch.QueueDeclare(
		"",
		false,
		true,
		true,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to declare reply queue: %w", err)
	}

	s.replyQueue = q

	msgs, err := s.ch.Consume(
		q.Name,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to register a consumer on reply queue: %w", err)
	}

	go func() {
		for msg := range msgs {
			respChanValue, ok := s.correlationMap.LoadAndDelete(msg.CorrelationId)
			if ok {
				respChan := respChanValue.(chan Response)

				resp := Response{
					Body:    msg.Body,
					Headers: msg.Headers,
				}

				if errMsg, exists := msg.Headers["-x-error"]; exists {
					resp.Error = fmt.Errorf("%v", errMsg)
				}

				respChan <- resp
			}
		}
	}()

	return nil
}

func (s *RMQService) Disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.connected {
		return
	}

	if s.ch != nil {
		s.ch.Close()
	}

	if s.conn != nil {
		s.conn.Close()
	}

	s.exitCh <- true
	s.connected = false
	log.Println("Disconnected from RabbitMQ")
}

func (s *RMQService) Listen() {
	s.mu.Lock()
	if !s.connected || s.configs.Queue == "" {
		s.mu.Unlock()
		log.Println("Cannot listen: not connected or queue not specified")
		return
	}
	s.mu.Unlock()

	setupConsumer := func() (<-chan amqp.Delivery, error) {
		return s.ch.Consume(
			s.configs.Queue,
			"",
			false,
			false,
			false,
			false,
			nil,
		)
	}

	msgs, err := setupConsumer()
	if err != nil {
		log.Printf("Failed to register a consumer: %v", err)
		return
	}

	log.Printf("[*] Awaiting RPC requests on queue: %s", s.configs.Queue)

	consumerActive := true
	reconnectCh := make(chan bool)

	go func() {
		for {
			select {
			case <-s.exitCh:
				return
			case <-reconnectCh:
				s.mu.Lock()
				if !s.connected {
					s.mu.Unlock()
					continue
				}

				newMsgs, err := setupConsumer()
				if err != nil {
					log.Printf("Failed to re-register consumer: %v", err)
					s.mu.Unlock()

					time.Sleep(s.configs.ReconnectDelay)
					reconnectCh <- true
					continue
				}

				msgs = newMsgs
				consumerActive = true
				s.mu.Unlock()

				log.Println("Consumer reconnected successfully")
			}
		}
	}()

	go func() {
		for {
			select {
			case <-s.exitCh:
				return
			case msg, ok := <-msgs:
				if !ok {
					if consumerActive {
						log.Println("Consumer channel closed, reconnecting...")
						consumerActive = false
						reconnectCh <- true
					}
					continue
				}

				handler, exists := s.configs.Routes[msg.RoutingKey]
				if !exists {
					log.Printf("No handler for routing key: %s", msg.RoutingKey)
					msg.Ack(false)
					continue
				}

				resp, err := handler(msg.Body)

				headers := make(amqp.Table)
				if err != nil {
					headers["-x-error"] = err.Error()
					log.Printf("Error processing message: %v", err)
				} else {
					headers["done"] = "ok"
				}

				if msg.ReplyTo != "" {
					s.mu.Lock()
					if s.connected {
						err = s.ch.Publish(
							"",
							msg.ReplyTo,
							false,
							false,
							amqp.Publishing{
								ContentType:   "text/json",
								CorrelationId: msg.CorrelationId,
								Body:          resp,
								Headers:       headers,
							})

						if err != nil {
							log.Printf("Failed to publish reply: %v", err)
						}
					}
					s.mu.Unlock()
				}
				msg.Ack(false)
			}
		}
	}()
	<-s.exitCh
}

// PublishEvent отправляет событие без ожидания ответа
func (s *RMQService) PublishEvent(topic string, message interface{}) error {
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		return fmt.Errorf("not connected to RabbitMQ")
	}
	s.mu.Unlock()

	msgBytes, err := EncodeMsg(message)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < s.configs.MaxRetryAttempts; attempt++ {
		s.mu.Lock()
		if !s.connected {
			s.mu.Unlock()
			lastErr = fmt.Errorf("connection lost")

			time.Sleep(s.configs.RetryDelay)
			continue
		}

		err = s.ch.Publish(
			s.configs.Exchange,
			topic,
			false,
			false,
			amqp.Publishing{
				ContentType: "text/json",
				Body:        msgBytes,
			})
		s.mu.Unlock()

		if err == nil {
			return nil
		}

		lastErr = err
		log.Printf("Failed to publish event (attempt %d/%d): %v",
			attempt+1, s.configs.MaxRetryAttempts, err)

		if attempt < s.configs.MaxRetryAttempts-1 {
			time.Sleep(s.configs.RetryDelay)
		}
	}

	return fmt.Errorf("failed to publish event after %d attempts: %w",
		s.configs.MaxRetryAttempts, lastErr)
}

func (s *RMQService) SendWithResponse(ctx context.Context, topic string, message interface{}, result interface{}) error {
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		return fmt.Errorf("not connected to RabbitMQ")
	}
	s.mu.Unlock()

	msgBytes, err := EncodeMsg(message)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	correlationId := generateCorrelationID()

	respChan := make(chan Response, 1)

	s.correlationMap.Store(correlationId, respChan)

	defer func() {
		s.correlationMap.Delete(correlationId)
		close(respChan)
	}()

	var lastErr error
	for attempt := 0; attempt < s.configs.MaxRetryAttempts; attempt++ {
		s.mu.Lock()
		if !s.connected {
			s.mu.Unlock()
			lastErr = fmt.Errorf("connection lost")

			time.Sleep(s.configs.RetryDelay)
			continue
		}

		err = s.ch.Publish(
			s.configs.Exchange,
			topic,
			false,
			false,
			amqp.Publishing{
				ContentType:   "text/json",
				Body:          msgBytes,
				CorrelationId: correlationId,
				ReplyTo:       s.replyQueue.Name,
			})
		s.mu.Unlock()

		if err == nil {
			break
		}

		lastErr = err
		log.Printf("Failed to publish message (attempt %d/%d): %v",
			attempt+1, s.configs.MaxRetryAttempts, err)

		if attempt == s.configs.MaxRetryAttempts-1 {
			return fmt.Errorf("failed to publish message after %d attempts: %w",
				s.configs.MaxRetryAttempts, lastErr)
		}

		time.Sleep(s.configs.RetryDelay)
	}

	select {
	case resp := <-respChan:

		if resp.Error != nil {
			return fmt.Errorf("error from server: %w", resp.Error)
		}

		if result != nil {
			err := DecodeMsg(resp.Body, result)
			if err != nil {
				return fmt.Errorf("failed to decode response: %w", err)
			}
		}

		return nil

	case <-ctx.Done():
		return fmt.Errorf("request timed out or context cancelled: %w", ctx.Err())
	}
}
