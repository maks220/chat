package main

import (
	 "encoding/json"
    "fmt"
    "net/http"
    "os"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

type Message struct {
	Username string `json:"username"`
	Text     string `json:"text"`
	Time     string `json:"time"`
	Color    string `json:"color"`
}

type Client struct {
	conn       *websocket.Conn
	send       chan []byte
	pingPeriod time.Duration
}

type Hub struct {
	clients    map[*Client]bool // Map всех клиентов
	broadcast  chan []byte      // Канал для broadcast сообщений
	register   chan *Client     // Канал для регистрации новых клиентов
	unregister chan *Client     // Канал для удаления клиентов
	mutex      sync.Mutex
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.mutex.Lock()
			h.clients[client] = true
			h.mutex.Unlock()

		case client := <-h.unregister:
			h.mutex.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mutex.Unlock()

		case message := <-h.broadcast:
			h.mutex.Lock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.mutex.Unlock()
		}
	}
}

var hub = &Hub{
	broadcast:  make(chan []byte),
	register:   make(chan *Client),
	unregister: make(chan *Client),
	clients:    make(map[*Client]bool),
}

var upgrader = websocket.Upgrader{
	CheckOrigin:      func(r *http.Request) bool { return true },
	HandshakeTimeout: 10 * time.Second,
}

func homePage(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}

func wsHandler(w http.ResponseWriter, r *http.Request) {

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("Ошибка апгрейда:", err)
		return
	}

	client := &Client{
		conn:       conn,
		send:       make(chan []byte, 256),
		pingPeriod: 60 * time.Second,
	}

	hub.register <- client

	go client.writePump()
	go client.readPump()
}

func main() {
	    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

	go hub.run()
	http.HandleFunc("/", homePage)
	http.HandleFunc("/ws", wsHandler)

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("."))))

    fmt.Printf("Сервер запущен на порту :%s\n", port)
    err := http.ListenAndServe(":"+port, nil)
    if err != nil {
        panic(err)
    }
}

func (c *Client) readPump() {
    defer func() {
        hub.unregister <- c
        c.conn.Close()
        fmt.Println("Клиент отключился (read)")
    }()

    for {
        _, message, err := c.conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
                fmt.Printf("Ошибка чтения: %v\n", err)
            }
            break
        }

        var username, text string

        // Пытаемся распарсить как JSON
        var incomingData map[string]string
        if err := json.Unmarshal(message, &incomingData); err == nil {
            // Если это JSON - берем данные из него
            username = incomingData["username"]
            text = incomingData["text"]
        } else {
            // Если не JSON - считаем это plain text
            username = "Аноним"
            text = string(message)
        }

        if username == "" {
            username = "Аноним"
        }

        // Создаем структуру сообщения
        msg := Message{
            Username: username,
            Text:     text,
            Time:     time.Now().Format("15:04"),
        }

        // Конвертируем в JSON
        jsonMessage, err := json.Marshal(msg)
        if err != nil {
            fmt.Printf("Ошибка маршалинга JSON: %v\n", err)
            continue
        }

        fmt.Printf("Получено сообщение от %s: %s\n", msg.Username, msg.Text)
        hub.broadcast <- jsonMessage
    }
}

func (c *Client) writePump() {
    ticker := time.NewTicker(c.pingPeriod)
    defer func() {
        ticker.Stop()
        c.conn.Close()
        fmt.Println("Клиент отключился (write)")
    }()

    for {
        select {
        case message, ok := <-c.send:
            c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
            if !ok {
                c.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }

            // ЗАМЕНИ ЭТОТ БЛОК:
            err := c.conn.WriteMessage(websocket.TextMessage, message)
            if err != nil {
                fmt.Printf("Ошибка отправки сообщения: %v\n", err)
                return
            }

        case <-ticker.C:
            c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
            if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
                return
            }
        }
    }
}