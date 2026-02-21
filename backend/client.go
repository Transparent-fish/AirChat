package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	hub        *Hub
	conn       *websocket.Conn // websocket链接
	send       chan Message    // 消息
	Username   string          // 用户昵称
	Avatar     string          // 头像
	Role       string          // 角色: user, admin
	Identifier string          // 唯一标识 (IP + Port)
}

// 读发的消息
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	for {
		// 读取消息
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("error: %v", err)
			break
		}

		// 尝试解析为 JSON
		var incoming struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Avatar  string `json:"avatar"`
			Name    string `json:"name"`
		}

		err = json.Unmarshal(payload, &incoming)
		if err != nil {
			// 如果不是 JSON，当作普通文本处理
			incoming.Type = "user"
			incoming.Content = string(payload)
		}

		// 更新用户信息（如果前端传了）
		if incoming.Name != "" {
			c.Username = incoming.Name
		}
		if incoming.Avatar != "" {
			c.Avatar = incoming.Avatar
		}

		// 查询数据库确认用户状态
		var user User
		if err := db.Where("username = ?", c.Username).First(&user).Error; err == nil {
			if user.IsBanned {
				c.sendSystemMsg("您的账号已被封禁")
				c.conn.Close()
				break
			}
			if user.IsMuted {
				c.sendSystemMsg("您已被禁言，无法发送消息")
				continue
			}
		}

		// 处理指令
		if strings.HasPrefix(incoming.Content, "/") {
			c.handleCommand(incoming.Content)
			continue
		}

		// 广播消息
		msg := Message{
			Sender:     c.Identifier,
			SenderName: c.Username,
			Avatar:     c.Avatar,
			Content:    incoming.Content,
			Time:       time.Now().Format("15:04"),
			Type:       "user",
			Role:       c.Role,
		}
		c.hub.broadcast <- msg
	}
}

// 指令处理
func (c *Client) handleCommand(content string) {
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return
	}

	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "/nick":
		if len(args) > 0 {
			oldName := c.Username
			c.Username = strings.Join(args, " ")
			c.sendSystemMsg(fmt.Sprintf("用户 %s 已更名为 %s", oldName, c.Username))
		}
	case "/admin":
		if c.Role == "system" {
			c.sendSystemMsg("您已经是系统最高管理权限，无需认证。")
			return
		}
		var adminConfig Config
		db.Where("key = ?", "admin_password").First(&adminConfig)
		// 校验密码
		if len(args) > 0 && args[0] == adminConfig.Value {
			c.Role = "admin"
			// 更新数据库中的用户角色
			db.Model(&User{}).Where("username = ?", c.Username).Update("role", "admin")
			c.sendSystemMsg("管理员认证成功！您可以访问左侧导航栏的「管理面板」功能。")
			c.send <- Message{
				Type: "role_update",
				Role: "admin",
			}
		} else {
			c.sendSystemMsg("管理员验证失败：密码错误")
		}
	case "/system":
		var sysConfig Config
		db.Where("key = ?", "system_password").First(&sysConfig)
		if len(args) > 0 && args[0] == sysConfig.Value {
			c.Role = "system"
			db.Model(&User{}).Where("username = ?", c.Username).Update("role", "system")
			c.sendSystemMsg("超级系统权认证成功！已开启全局管控权限。")
			c.send <- Message{
				Type: "role_update",
				Role: "system",
			}
		} else {
			c.sendSystemMsg("系统权限验证失败：密码错误")
		}
	default:
		c.sendSystemMsg("未知指令: " + cmd)
	}
}

// 发送系统私聊消息
func (c *Client) sendSystemMsg(content string) {
	msg := Message{
		Sender:     "system",
		SenderName: "系统",
		Content:    content,
		Time:       time.Now().Format("15:04"),
		Type:       "system",
		Role:       "user",
	}
	c.send <- msg
}

// 打印发的消息
func (c *Client) writPump() {
	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				//hub把通道关了
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteJSON(message); err != nil {
				return
			}
		}
	}
}
