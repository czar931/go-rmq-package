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

	// Применяем настройки по умолчанию
	config = applyDefaultConfig(config)
	s.configs = config

	// Устанавливаем соединение
	if !s.connectInternal() {
		return false
	}

	// Настраиваем мониторинг состояния соединения
	go s.monitorConnection()

	return true
}

// connectInternal выполняет внутреннее подключение к RabbitMQ
func (s *RMQService) connectInternal() bool {
	// Формируем строку подключения
	connectionString := fmt.Sprintf("amqp://%s:%s@%s:5672",
		s.configs.Login, s.configs.Password, s.configs.Host)

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

	// Устанавливаем QoS
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

	// Объявляем обмен, если он указан
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

	// Настраиваем очередь для ответов
	err = s.setupReplyQueue()
	if err != nil {
		log.Printf("Failed to setup reply queue: %v", err)
		s.ch.Close()
		s.conn.Close()
		return false
	}

	// Если указана основная очередь, объявляем ее и привязываем к маршрутам
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

		// Привязываем очередь к каждому маршруту
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

// monitorConnection отслеживает состояние соединения и канала
// и выполняет повторное подключение при необходимости
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

// reconnect выполняет повторное подключение к RabbitMQ
func (s *RMQService) reconnect() {
	s.mu.Lock()
	defer func() {
		s.reconnecting = false
		s.mu.Unlock()
	}()

	// Закрываем существующие соединения, если они есть
	if s.ch != nil {
		s.ch.Close()
	}

	if s.conn != nil {
		s.conn.Close()
	}

	// Повторные попытки подключения
	for attempt := 1; attempt <= s.configs.MaxRetryAttempts; attempt++ {
		log.Printf("Reconnection attempt %d/%d", attempt, s.configs.MaxRetryAttempts)

		if s.connectInternal() {
			return
		}

		// Ждем перед следующей попыткой
		time.Sleep(s.configs.ReconnectDelay)
	}

	log.Printf("Failed to reconnect after %d attempts", s.configs.MaxRetryAttempts)
}

// setupReplyQueue настраивает очередь для получения ответов
func (s *RMQService) setupReplyQueue() error {
	q, err := s.ch.QueueDeclare(
		"",    // пустое имя = автоматически сгенерированное
		false, // не durable
		true,  // автоудаление
		true,  // эксклюзивная
		false, // no-wait
		nil,   // аргументы
	)
	if err != nil {
		return fmt.Errorf("failed to declare reply queue: %w", err)
	}

	s.replyQueue = q

	// Устанавливаем потребителя для ответов
	msgs, err := s.ch.Consume(
		q.Name, // имя очереди
		"",     // потребитель
		true,   // auto-ack
		false,  // эксклюзивный
		false,  // no-local
		false,  // no-wait
		nil,    // аргументы
	)
	if err != nil {
		return fmt.Errorf("failed to register a consumer on reply queue: %w", err)
	}

	// Обрабатываем входящие ответы
	go func() {
		for msg := range msgs {
			respChanValue, ok := s.correlationMap.LoadAndDelete(msg.CorrelationId)
			if ok {
				respChan := respChanValue.(chan Response)

				// Создаем объект ответа
				resp := Response{
					Body:    msg.Body,
					Headers: msg.Headers,
				}

				// Проверяем наличие ошибки в заголовках
				if errMsg, exists := msg.Headers["-x-error"]; exists {
					resp.Error = fmt.Errorf("%v", errMsg)
				}

				// Отправляем ответ
				respChan <- resp
			}
		}
	}()

	return nil
}

// Disconnect закрывает соединение с RabbitMQ
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

// Listen начинает слушать сообщения из очереди
func (s *RMQService) Listen() {
	s.mu.Lock()
	if !s.connected || s.configs.Queue == "" {
		s.mu.Unlock()
		log.Println("Cannot listen: not connected or queue not specified")
		return
	}
	s.mu.Unlock()

	// Функция для установки потребителя
	setupConsumer := func() (<-chan amqp.Delivery, error) {
		return s.ch.Consume(
			s.configs.Queue, // имя очереди
			"",              // потребитель
			false,           // не auto-ack, будем подтверждать вручную
			false,           // не эксклюзивный
			false,           // no-local
			false,           // no-wait
			nil,             // аргументы
		)
	}

	// Первоначальная настройка потребителя
	msgs, err := setupConsumer()
	if err != nil {
		log.Printf("Failed to register a consumer: %v", err)
		return
	}

	log.Printf("[*] Awaiting RPC requests on queue: %s", s.configs.Queue)

	// Флаг для отслеживания состояния и канал для сигнализации о переподключении
	consumerActive := true
	reconnectCh := make(chan bool)

	// Слушаем сигналы о необходимости переподключить потребителя
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

				// Пытаемся установить нового потребителя
				newMsgs, err := setupConsumer()
				if err != nil {
					log.Printf("Failed to re-register consumer: %v", err)
					s.mu.Unlock()

					// Ждем перед повторной попыткой
					time.Sleep(s.configs.ReconnectDelay)
					reconnectCh <- true
					continue
				}

				// Обновляем канал сообщений
				msgs = newMsgs
				consumerActive = true
				s.mu.Unlock()

				log.Println("Consumer reconnected successfully")
			}
		}
	}()

	// Обрабатываем входящие сообщения
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

				// Проверяем наличие обработчика для маршрута
				handler, exists := s.configs.Routes[msg.RoutingKey]
				if !exists {
					log.Printf("No handler for routing key: %s", msg.RoutingKey)
					msg.Ack(false)
					continue
				}

				// Обрабатываем сообщение
				resp, err := handler(msg.Body)

				// Подготавливаем заголовки для ответа
				headers := make(amqp.Table)
				if err != nil {
					headers["-x-error"] = err.Error()
					log.Printf("Error processing message: %v", err)
				} else {
					headers["done"] = "ok"
				}

				// Если указан ReplyTo, отправляем ответ
				if msg.ReplyTo != "" {
					s.mu.Lock()
					if s.connected {
						err = s.ch.Publish(
							"",          // exchange
							msg.ReplyTo, // routing key
							false,       // mandatory
							false,       // immediate
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

				// Подтверждаем обработку сообщения
				msg.Ack(false)
			}
		}
	}()

	// Блокируемся до получения сигнала завершения
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

	// Кодируем сообщение в JSON
	msgBytes, err := EncodeMsg(message)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	// Пытаемся отправить сообщение с повторами при неудаче
	var lastErr error
	for attempt := 0; attempt < s.configs.MaxRetryAttempts; attempt++ {
		s.mu.Lock()
		if !s.connected {
			s.mu.Unlock()
			lastErr = fmt.Errorf("connection lost")

			// Ждем перед повтором
			time.Sleep(s.configs.RetryDelay)
			continue
		}

		err = s.ch.Publish(
			s.configs.Exchange, // exchange
			topic,              // routing key
			false,              // mandatory
			false,              // immediate
			amqp.Publishing{
				ContentType: "text/json",
				Body:        msgBytes,
				// Не указываем ReplyTo и CorrelationId, так как ответ не нужен
			})
		s.mu.Unlock()

		if err == nil {
			return nil // Успешно отправлено
		}

		lastErr = err
		log.Printf("Failed to publish event (attempt %d/%d): %v",
			attempt+1, s.configs.MaxRetryAttempts, err)

		// Если это не последняя попытка, ждем перед повтором
		if attempt < s.configs.MaxRetryAttempts-1 {
			time.Sleep(s.configs.RetryDelay)
		}
	}

	return fmt.Errorf("failed to publish event after %d attempts: %w",
		s.configs.MaxRetryAttempts, lastErr)
}

// SendWithResponse отправляет сообщение и ожидает ответа
func (s *RMQService) SendWithResponse(ctx context.Context, topic string, message interface{}, result interface{}) error {
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		return fmt.Errorf("not connected to RabbitMQ")
	}
	s.mu.Unlock()

	// Кодируем сообщение в JSON
	msgBytes, err := EncodeMsg(message)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	// Создаем уникальный идентификатор корреляции
	correlationId := generateCorrelationID()

	// Канал для получения ответа
	respChan := make(chan Response, 1)

	// Сохраняем канал в карте корреляции
	s.correlationMap.Store(correlationId, respChan)

	// Функция очистки, которая выполняется в конце
	defer func() {
		s.correlationMap.Delete(correlationId)
		close(respChan)
	}()

	// Пытаемся отправить сообщение с повторами при неудаче
	var lastErr error
	for attempt := 0; attempt < s.configs.MaxRetryAttempts; attempt++ {
		s.mu.Lock()
		if !s.connected {
			s.mu.Unlock()
			lastErr = fmt.Errorf("connection lost")

			// Ждем перед повтором
			time.Sleep(s.configs.RetryDelay)
			continue
		}

		err = s.ch.Publish(
			s.configs.Exchange, // exchange
			topic,              // routing key
			false,              // mandatory
			false,              // immediate
			amqp.Publishing{
				ContentType:   "text/json",
				Body:          msgBytes,
				CorrelationId: correlationId,
				ReplyTo:       s.replyQueue.Name,
			})
		s.mu.Unlock()

		if err == nil {
			break // Успешно отправлено
		}

		lastErr = err
		log.Printf("Failed to publish message (attempt %d/%d): %v",
			attempt+1, s.configs.MaxRetryAttempts, err)

		// Если это последняя попытка, возвращаем ошибку
		if attempt == s.configs.MaxRetryAttempts-1 {
			return fmt.Errorf("failed to publish message after %d attempts: %w",
				s.configs.MaxRetryAttempts, lastErr)
		}

		// Ждем перед повтором
		time.Sleep(s.configs.RetryDelay)
	}

	// Ожидаем ответа с контекстом и таймаутом
	select {
	case resp := <-respChan:
		// Проверяем на ошибку в ответе
		if resp.Error != nil {
			return fmt.Errorf("error from server: %w", resp.Error)
		}

		// Декодируем ответ, если указан результат
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
