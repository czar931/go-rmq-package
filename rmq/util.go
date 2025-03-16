package rmq

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// EncodeMsg преобразует объект в JSON байты
func EncodeMsg(msg interface{}) ([]byte, error) {
	reply, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return reply, nil
}

// DecodeMsg декодирует JSON байты в указанный объект
func DecodeMsg(body []byte, v interface{}) error {
	err := json.Unmarshal(body, v)
	if err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return nil
}

// Старый вариант DecodeMsg с использованием дженериков
// оставлен для обратной совместимости
func DecodeMsg2[T any](body []byte) (T, error) {
	var msg T
	err := json.Unmarshal(body, &msg)
	if err != nil {
		return msg, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return msg, nil
}

// generateCorrelationID создает уникальный идентификатор для корреляции запросов
func generateCorrelationID() string {
	// Создаем случайную строку из 32 символов
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 32

	// Инициализируем генератор случайных чисел с текущим временем
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	b := make([]byte, length)
	for i := range b {
		b[i] = charset[r.Intn(len(charset))]
	}

	return string(b)
}

// logError логирует ошибку, если она не равна nil
func logError(err error, msg string) {
	if err != nil {
		log.Printf("%s: %v", msg, err)
	}
}

// failOnError вызывает панику, если ошибка не равна nil
func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
		panic(fmt.Sprintf("%s: %s", msg, err))
	}
}

// mergeStringMaps объединяет два отображения строк
func mergeStringMaps(m1, m2 map[string]string) map[string]string {
	result := make(map[string]string)

	// Копируем все значения из первого отображения
	for k, v := range m1 {
		result[k] = v
	}

	// Добавляем или заменяем значения из второго отображения
	for k, v := range m2 {
		result[k] = v
	}

	return result
}

// retry выполняет функцию с повторными попытками
func retry(attempts int, delay time.Duration, fn func() error) error {
	var err error

	for i := 0; i < attempts; i++ {
		err = fn()
		if err == nil {
			return nil
		}

		log.Printf("Attempt %d failed: %v. Retrying in %v...", i+1, err, delay)
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", attempts, err)
}

// waitWithTimeout ожидает завершения функции или истечения таймаута
func waitWithTimeout(timeout time.Duration, fn func() error) error {
	result := make(chan error, 1)

	// Запускаем функцию в отдельной горутине
	go func() {
		result <- fn()
	}()

	// Ожидаем результат или таймаут
	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("operation timed out after %v", timeout)
	}
}

// createHeadersTable создает таблицу заголовков AMQP из отображения строк
func createHeadersTable(headers map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range headers {
		result[k] = v
	}
	return result
}

// extractErrorFromHeaders извлекает сообщение об ошибке из заголовков
func extractErrorFromHeaders(headers map[string]interface{}) error {
	if errMsg, exists := headers["-x-error"]; exists {
		return fmt.Errorf("%v", errMsg)
	}
	return nil
}
