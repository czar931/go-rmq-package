package rmq

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// Converts the object into JSON bytes
func EncodeMsg(msg interface{}) ([]byte, error) {
	reply, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}
	return reply, nil
}

// Decodes JSON bytes into the specified object
func DecodeMsg(body []byte, v interface{}) error {
	err := json.Unmarshal(body, v)
	if err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return nil
}

// Creates a unique identifier to correlate queries
func generateCorrelationID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 32

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	b := make([]byte, length)
	for i := range b {
		b[i] = charset[r.Intn(len(charset))]
	}

	return string(b)
}

// Logs the error if it is not equal to nil
func logError(err error, msg string) {
	if err != nil {
		log.Printf("%s: %v", msg, err)
	}
}

// Causes panic if the error is not equal to nil
func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
		panic(fmt.Sprintf("%s: %s", msg, err))
	}
}

// Combines two string mappings
func mergeStringMaps(m1, m2 map[string]string) map[string]string {
	result := make(map[string]string)

	for k, v := range m1 {
		result[k] = v
	}

	for k, v := range m2 {
		result[k] = v
	}

	return result
}

// Executes the function with retries
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

// Waits for the function to complete or for the timeout to expire
func waitWithTimeout(timeout time.Duration, fn func() error) error {
	result := make(chan error, 1)

	go func() {
		result <- fn()
	}()

	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("operation timed out after %v", timeout)
	}
}

// Creates an AMQP header table from a mapping of rows
func createHeadersTable(headers map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range headers {
		result[k] = v
	}
	return result
}

// Extracts the error message from the headers
func extractErrorFromHeaders(headers map[string]interface{}) error {
	if errMsg, exists := headers["-x-error"]; exists {
		return fmt.Errorf("%v", errMsg)
	}
	return nil
}
