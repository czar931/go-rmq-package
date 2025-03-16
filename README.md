RMQ is a lightweight wrapper over RabbitMQ for Go with support for automatic reconnection, retrays and a handy API for sending messages with or without waiting for a reply.
Features

🔄 Automatic reconnection: RMQ automatically reconnects on failures
🔁 Retrays on sending: Configurable number of retries to send messages
📤 Event sending: Send messages without waiting for a response
📥 RPC-style requests: Sending messages with response pending
⏱️ Timeout management: Customisable timeouts for different operations

**Install**

```go get github.com/yourusername/rmq```

**Quick start**
```
func main() {
    // Create an instance of RMQ service
    rmqService := rmq.NewRMQService()
    
    // Configuring the configuration
    config := rmq.NewDefaultConfig()
    config.Host = "localhost"
    config.Login = "guest"
    config.Password = "guest"
    config.Exchange = "my_exchange"
    config.Queue = "my_queue"
    
    // Connecting to RabbitMQ
    if !rmqService.Connect(config) {
        log.Fatal("Failed to connect to RabbitMQ")
    }
    defer rmqService.Disconnect()
    
    // Publish an event without waiting for a response
    err := rmqService.PublishEvent("user.event", map[string]interface{}{
        "user_id": 123,
        "action": "logged_in",
    })
    if err != nil {
        log.Printf("Error publishing event: %v", err)
    }
    
    // Sending an enquiry and waiting for a response
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    var response map[string]interface{}
    err = rmqService.SendWithResponse(ctx, "user.get", map[string]string{
        "user_id": "456",
    }, &response)
    if err != nil {
        log.Printf("Error sending request: %v", err)
    } else {
        log.Printf("Received response: %v", response)
    }
}
```

**Configuring route handlers**

```
routes := map[string]func([]byte) ([]byte, error){
    "user.get": func(body []byte) ([]byte, error) {
        var request map[string]interface{}
        if err := rmq.DecodeMsg(body, &request); err != nil {
            return nil, err
        }
        
        // Обработка запроса...
        
        return rmq.EncodeMsg(map[string]interface{}{
            "status": "success",
            "data": map[
                ```