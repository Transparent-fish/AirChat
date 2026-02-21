package main

import (
	"time"

	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
	Username  string         `gorm:"uniqueIndex;type:varchar(12)" json:"username"`
	Password  string         `json:"-"` // 不在 JSON 中返回密码
	Avatar    string         `json:"avatar"`
	Role      string         `json:"role"`
	IsMuted   bool           `json:"is_muted"`  // 改动: 禁言
	IsBanned  bool           `json:"is_banned"` // 改动: 封禁
}

// Message 消息模型
type Message struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time      `json:"-"`
	UpdatedAt  time.Time      `json:"-"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
	Sender     string         `json:"sender"`      // 发送者ID/地址 (Identifier)
	SenderName string         `json:"sender_name"` // 发送者昵称
	Avatar     string         `json:"avatar"`      // 头像
	Content    string         `json:"content"`     // 内容
	Time       string         `json:"time"`        // 格式化时间 "15:04"
	Type       string         `json:"type"`        // 消息类型: user, system
	Role       string         `json:"role"`        // 角色: user, admin
}

// IPBan IP封禁模型
type IPBan struct {
	IP      string `gorm:"primarykey" json:"ip"`
	IsRange bool   `json:"is_range"` // 是否是网段 (CIDR)
}

// Config 配置模型
type Config struct {
	Key   string `gorm:"primarykey" json:"key"`
	Value string `json:"value"`
}
