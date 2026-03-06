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
				close(client.send)
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// 根据用户名断开在线用户连接
func (h *Hub) disconnectByUsername(username string) {
	for client := range h.clients {
		if client.Username == username {
			// 发送强制断开消息
			select {
			case client.send <- Message{
				Type:    "force_disconnect",
				Content: "您的账号已被管理员处理，连接已断开",
			}:
			default:
			}
			// 关闭连接
			client.conn.Close()
		}
	}
}

// 根据IP断开在线用户连接
func (h *Hub) disconnectByIP(ip string) {
	for client := range h.clients {
		if client.IP == ip {
			// 发送强制断开消息
			select {
			case client.send <- Message{
				Type:    "force_disconnect",
				Content: "您的IP已被封禁，连接已断开",
			}:
			default:
			}
			// 关闭连接
			client.conn.Close()
		}
	}
}

// 根据IP列表检查并断开被封IP的连接（用于范围封禁等场景）
func (h *Hub) disconnectBannedIPs() {
	for client := range h.clients {
		if isIPBanned(client.IP) {
			select {
			case client.send <- Message{
				Type:    "force_disconnect",
				Content: "您的IP已被封禁，连接已断开",
			}:
			default:
			}
			client.conn.Close()
		}
	}
}
