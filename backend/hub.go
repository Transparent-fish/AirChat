package main

// 2. 喵喵喵
type Hub struct {
	clients    map[*Client]bool
	broadcast  chan Message // 广播通道
	register   chan *Client // 新用户登记通道
	unregister chan *Client // 用户注销通道
}

// 3. init Hub
func newHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan Message),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Hub) run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				// 等 Client 写完补上：close(client.send)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// 等 Client 写完补上：close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}
