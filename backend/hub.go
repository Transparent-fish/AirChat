package main

import "sync"

// 2. 喵喵喵
type Hub struct {
	mu         sync.RWMutex
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
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// 并发场景下不在此处直接修改 map。后续写通道满时 client 也会自行退出触发 unregister
				}
			}
			h.mu.RUnlock()
		}
	}
}

// 根据用户名断开在线用户连接
func (h *Hub) disconnectByUsername(username string) {
	var targets []*Client
	h.mu.RLock()
	for client := range h.clients {
		if client.Username == username {
			targets = append(targets, client)
		}
	}
	h.mu.RUnlock()

	for _, client := range targets {
		// 发送强制断开消息
		select {
		case client.send <- Message{
			Type:    "force_disconnect",
			Content: "您的账号已被管理员处理，连接已断开",
		}:
		default:
		}
		// 只发送退出信号，不直接关闭，由 goroutine 自行退出
		go client.conn.Close()
	}
}

// 根据IP断开在线用户连接
func (h *Hub) disconnectByIP(ip string) {
	var targets []*Client
	h.mu.RLock()
	for client := range h.clients {
		if client.IP == ip {
			targets = append(targets, client)
		}
	}
	h.mu.RUnlock()

	for _, client := range targets {
		// 发送强制断开消息
		select {
		case client.send <- Message{
			Type:    "force_disconnect",
			Content: "您的IP已被封禁，连接已断开",
		}:
		default:
		}
		go client.conn.Close()
	}
}

// 根据IP列表检查并断开被封IP的连接（用于范围封禁等场景）
func (h *Hub) disconnectBannedIPs() {
	var targets []*Client
	h.mu.RLock()
	for client := range h.clients {
		if isIPBanned(client.IP) {
			targets = append(targets, client)
		}
	}
	h.mu.RUnlock()

	for _, client := range targets {
		select {
		case client.send <- Message{
			Type:    "force_disconnect",
			Content: "您的IP已被封禁，连接已断开",
		}:
		default:
		}
		go client.conn.Close()
	}
}
