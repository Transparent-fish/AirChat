package main

import (
	"archive/zip"
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

//go:embed dist
var frontendStatic embed.FS

var db *gorm.DB

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var jwtKey = []byte("air_chat_secret_key_12345") // 在实际生产中应使用环境变量

type Claims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// 获取请求的真实IP
func getClientIP(c *gin.Context) string {
	ip := c.ClientIP()
	if ip == "::1" {
		ip = "127.0.0.1"
	}
	host, _, err := net.SplitHostPort(ip)
	if err == nil {
		return host
	}
	return ip
}

// 检查IP是否被封禁
func isIPBanned(ip string) bool {
	if ip == "" {
		return false
	}
	var bans []IPBan
	db.Find(&bans)

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	for _, ban := range bans {
		if ban.IsRange {
			// 支持 CIDR 或 IP-IP 区间模式
			if strings.Contains(ban.IP, "/") {
				_, ipNet, err := net.ParseCIDR(ban.IP)
				if err == nil && ipNet.Contains(parsedIP) {
					return true
				}
			} else if strings.Contains(ban.IP, "-") {
				parts := strings.Split(ban.IP, "-")
				if len(parts) == 2 {
					start := net.ParseIP(strings.TrimSpace(parts[0])).To4()
					end := net.ParseIP(strings.TrimSpace(parts[1])).To4()
					target := parsedIP.To4()

					if start != nil && end != nil && target != nil {
						if bytes.Compare(target, start) >= 0 && bytes.Compare(target, end) <= 0 {
							return true
						}
					}
				}
			}
		} else {
			if ban.IP == ip {
				return true
			}
		}
	}
	return false
}

func main() {
	// 初始化数据库
	var err error
	db, err = gorm.Open(sqlite.Open("air_chat.db"), &gorm.Config{})
	if err != nil {
		log.Fatalf("无法连接数据库: %v", err)
	}

	// 自动迁移
	db.AutoMigrate(&User{}, &Message{}, &IPBan{}, &Config{}, &PendingUpload{})

	// 初始化默认管理员和系统管理员密码
	var adminConfig Config
	if err := db.Where("key = ?", "admin_password").First(&adminConfig).Error; err != nil {
		db.Create(&Config{Key: "admin_password", Value: "admin123"})
	}
	var systemConfig Config
	if err := db.Where("key = ?", "system_password").First(&systemConfig).Error; err != nil {
		db.Create(&Config{Key: "system_password", Value: "system123"})
	}

	// 初始化 Gin
	r := gin.Default()

	// 添加 CORS 中间件
	config := cors.DefaultConfig()
	config.AllowAllOrigins = true
	config.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	r.Use(cors.New(config))

	hub := newHub()
	go hub.run()

	// 注册接口
	r.POST("/api/register", func(c *gin.Context) {
		if isIPBanned(getClientIP(c)) {
			c.JSON(http.StatusForbidden, gin.H{"error": "您的IP已被封禁"})
			return
		}

		var req struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		// 验证用户名规则：字母/数字/下划线，不超过12位
		match, _ := regexp.MatchString("^[a-zA-Z0-9_]{1,12}$", req.Username)
		if !match {
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户名不符合规则（仅限12位以内字母/数字/下划线）"})
			return
		}

		// 检查用户名是否重复 (包含软删除的也会因为 unique_index 报错，但前面已经使用 Unscoped 彻底删除了)
		var existingUser User
		if err := db.Unscoped().Where("username = ?", req.Username).First(&existingUser).Error; err == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "用户名已存在"})
			return
		}

		// 密码加密
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)

		user := User{
			Username:      req.Username,
			Password:      string(hashedPassword),
			Avatar:        "https://api.dicebear.com/7.x/bottts/svg?seed=" + req.Username,
			Role:          "user",
			CanPlayGames:  true,
			CanShareFiles: true,
		}

		if err := db.Create(&user).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "注册成功"})
	})

	// 登录接口
	r.POST("/api/login", func(c *gin.Context) {
		if isIPBanned(getClientIP(c)) {
			c.JSON(http.StatusForbidden, gin.H{"error": "您的IP已被封禁"})
			return
		}

		var req struct {
			Username string `json:"username" binding:"required"`
			Password string `json:"password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		var user User
		if err := db.Where("username = ?", req.Username).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
			return
		}

		if user.IsBanned {
			c.JSON(http.StatusForbidden, gin.H{"error": "该账号已被封禁"})
			return
		}

		// 生成 JWT
		expirationTime := time.Now().Add(24 * time.Hour)
		claims := &Claims{
			Username: user.Username,
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(expirationTime),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		tokenString, err := token.SignedString(jwtKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "生成的 Token 失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"token":           tokenString,
			"username":        user.Username,
			"avatar":          user.Avatar,
			"role":            user.Role,
			"can_play_games":  user.CanPlayGames,
			"can_share_files": user.CanShareFiles,
			"system_level":    user.SystemLevel,
		})
	})

	// WebSocket 入口 (需要 Token)
	r.GET("/ws", func(c *gin.Context) {
		clientIP := getClientIP(c)
		if isIPBanned(clientIP) {
			c.JSON(http.StatusForbidden, gin.H{"error": "您的IP已被封禁"})
			return
		}

		tokenString := c.Query("token")
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未提供 Token"})
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效的 Token"})
			return
		}

		// 获取用户信息
		var user User
		if err := db.Where("username = ?", claims.Username).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户不存在"})
			return
		}

		if user.IsBanned {
			c.JSON(http.StatusForbidden, gin.H{"error": "该账号已被封禁"})
			return
		}

		// http -> WebSocket
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("upgrade error: %v", err)
			return
		}

		client := &Client{
			hub:        hub,
			conn:       conn,
			send:       make(chan Message, 256),
			Username:   user.Username,
			Avatar:     user.Avatar,
			Role:       user.Role,
			Identifier: conn.RemoteAddr().String(),
			IP:         clientIP,
		}

		client.hub.register <- client

		go client.writPump()
		go client.readPump()
	})

	// JWT 中间件
	authMiddleware := func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未提供 Token"})
			c.Abort()
			return
		}

		// 处理 "Bearer <token>"
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return jwtKey, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "无效的 Token"})
			c.Abort()
			return
		}

		c.Set("username", claims.Username)
		c.Next()
	}

	// 头像上传接口
	os.MkdirAll("./uploads", os.ModePerm)
	r.Static("/uploads", "./uploads")

	r.POST("/api/upload-avatar", authMiddleware, func(c *gin.Context) {
		file, err := c.FormFile("avatar")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无法获取文件"})
			return
		}

		username := c.MustGet("username").(string)
		filename := fmt.Sprintf("%d_%s", time.Now().Unix(), file.Filename)
		filepath := "./uploads/" + filename

		if err := c.SaveUploadedFile(file, filepath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败"})
			return
		}

		// 更新数据库中的用户头像
		avatarURL := "/uploads/" + filename
		if err := db.Model(&User{}).Where("username = ?", username).Update("avatar", avatarURL).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新数据库失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "上传成功",
			"url":     avatarURL,
		})
	})

	// ====== 文件共享路由 ======
	os.MkdirAll("./shared", os.ModePerm)
	r.Static("/shared", "./shared")
	os.MkdirAll("./temp_uploads", os.ModePerm) // 150MB超限审核存储目录

	// 上传文件夹（需登录）
	r.POST("/api/upload-folder", authMiddleware, func(c *gin.Context) {
		username := c.MustGet("username").(string)

		// 检查权限
		var user User
		if err := db.Where("username = ?", username).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户不存在"})
			return
		}
		if !user.CanShareFiles && user.Role == "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "您已被禁止共享文件"})
			return
		}

		folderName := c.PostForm("folderName")
		if folderName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "文件夹名不能为空"})
			return
		}
		folderName = strings.ReplaceAll(folderName, "..", "")
		folderName = strings.Trim(folderName, "/\\")

		form, err := c.MultipartForm()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无法解析表单"})
			return
		}
		files := form.File["files"]
		paths := form.Value["paths"]

		// 计算总大小
		var totalSize int64 = 0
		for _, file := range files {
			totalSize += file.Size
		}

		// > 150MB = 150 * 1024 * 1024 = 157286400 bytes
		needsApproval := totalSize > 157286400

		var destDir string
		if needsApproval {
			// 如果超过150MB，放入临时目录，等admin审核
			tempFolderName := fmt.Sprintf("%d_%s_%s", time.Now().Unix(), username, folderName)
			destDir = "./temp_uploads/" + tempFolderName
		} else {
			destDir = fmt.Sprintf("./shared/%s_%s", username, folderName)
		}

		os.MkdirAll(destDir, os.ModePerm)

		for i, file := range files {
			relPath := ""
			if i < len(paths) {
				relPath = paths[i]
			} else {
				relPath = file.Filename
			}
			parts := strings.SplitN(relPath, "/", 2)
			if len(parts) == 2 {
				relPath = parts[1]
			} else {
				relPath = file.Filename
			}
			destPath := destDir + "/" + relPath
			os.MkdirAll(filepath.Dir(destPath), os.ModePerm)
			c.SaveUploadedFile(file, destPath)
		}

		if needsApproval {
			db.Create(&PendingUpload{
				Username:   username,
				FolderName: folderName,
				TotalSize:  totalSize,
				Status:     "pending",
				TempPath:   destDir,
			})
			c.JSON(http.StatusOK, gin.H{"message": "上传成功，由于文件超过150MB，正在等待管理员审核", "status": "pending"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "上传成功", "status": "approved"})
	})

	// 上传单个文件（需登录）
	r.POST("/api/upload-file", authMiddleware, func(c *gin.Context) {
		username := c.MustGet("username").(string)

		// 检查权限
		var user User
		if err := db.Where("username = ?", username).First(&user).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "用户不存在"})
			return
		}
		if !user.CanShareFiles && user.Role == "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "您已被禁止共享文件"})
			return
		}

		file, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无法获取文件"})
			return
		}

		// 目标目录：./shared/<username>_uploads/
		destDir := fmt.Sprintf("./shared/%s_uploads", username)
		os.MkdirAll(destDir, os.ModePerm)

		// 安全处理文件名
		safeFileName := strings.ReplaceAll(file.Filename, "..", "")
		safeFileName = strings.Trim(safeFileName, "/\\")
		destPath := filepath.Join(destDir, safeFileName)

		// > 150MB 走审核
		if file.Size > 157286400 {
			tempDir := fmt.Sprintf("./temp_uploads/%d_%s_file", time.Now().Unix(), username)
			os.MkdirAll(tempDir, os.ModePerm)
			tempPath := filepath.Join(tempDir, safeFileName)
			if err := c.SaveUploadedFile(file, tempPath); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败"})
				return
			}
			db.Create(&PendingUpload{
				Username:   username,
				FolderName: "uploads/" + safeFileName,
				TotalSize:  file.Size,
				Status:     "pending",
				TempPath:   tempDir,
			})
			c.JSON(http.StatusOK, gin.H{"message": "上传成功，由于文件超过150MB，正在等待管理员审核", "status": "pending"})
			return
		}

		if err := c.SaveUploadedFile(file, destPath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "上传成功", "status": "approved"})
	})

	// 获取当前用户分享的文件夹列表（需登录）
	r.GET("/api/my-folders", authMiddleware, func(c *gin.Context) {
		username := c.MustGet("username").(string)
		prefix := username + "_"
		entries, err := os.ReadDir("./shared")
		if err != nil {
			c.JSON(http.StatusOK, []string{})
			return
		}
		var folders []string
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				folders = append(folders, strings.TrimPrefix(e.Name(), prefix))
			}
		}
		c.JSON(http.StatusOK, folders)
	})

	// 获取所有用户共享的文件夹（公共，需登录）
	r.GET("/api/shared-folders", authMiddleware, func(c *gin.Context) {
		subPath := c.Query("path")
		subPath = strings.ReplaceAll(subPath, "..", "")
		subPath = strings.Trim(subPath, "/\\")

		baseDir := "./shared"
		targetDir := baseDir
		if subPath != "" {
			targetDir = filepath.Join(baseDir, subPath)
		}

		entries, err := os.ReadDir(targetDir)
		if err != nil {
			c.JSON(http.StatusOK, []gin.H{})
			return
		}

		var result []gin.H
		for _, e := range entries {
			info, _ := e.Info()
			fullRelPath := e.Name()
			if subPath != "" {
				fullRelPath = subPath + "/" + e.Name()
			}

			item := gin.H{
				"name":   e.Name(),
				"path":   fullRelPath,
				"is_dir": e.IsDir(),
				"size":   int64(0),
			}

			if info != nil {
				item["size"] = info.Size()
			}

			if subPath == "" {
				parts := strings.SplitN(e.Name(), "_", 2)
				if len(parts) == 2 {
					item["owner"] = parts[0]
					item["name"] = parts[1]
				}
			}

			result = append(result, item)
		}

		if result == nil {
			result = []gin.H{}
		}
		c.JSON(http.StatusOK, result)
	})

	// 删除分享文件夹（需登录，只能删除自己的）
	r.DELETE("/api/delete-folder/:name", authMiddleware, func(c *gin.Context) {
		username := c.MustGet("username").(string)
		folderName := c.Param("name")
		folderName = strings.ReplaceAll(folderName, "..", "")
		folderName = strings.Trim(folderName, "/\\")
		targetDir := fmt.Sprintf("./shared/%s_%s", username, folderName)
		if err := os.RemoveAll(targetDir); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
	})

	// 下载文件夹（打包为 zip）
	r.GET("/api/download-folder", authMiddleware, func(c *gin.Context) {
		subPath := c.Query("path")
		subPath = strings.ReplaceAll(subPath, "..", "")
		subPath = strings.Trim(subPath, "/\\")

		baseDir := "./shared"
		targetDir := filepath.Join(baseDir, subPath)

		info, err := os.Stat(targetDir)
		if err != nil || !info.IsDir() {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到目录"})
			return
		}

		zipName := filepath.Base(targetDir)
		if strings.Contains(zipName, "_") {
			parts := strings.SplitN(zipName, "_", 2)
			if len(parts) == 2 {
				zipName = parts[1]
			}
		}

		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", zipName))
		c.Header("Content-Type", "application/zip")

		zw := zip.NewWriter(c.Writer)
		defer zw.Close()

		filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			relPath, err := filepath.Rel(targetDir, path)
			if err != nil {
				return err
			}

			return func() error {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()

				w, err := zw.Create(relPath)
				if err != nil {
					return err
				}

				_, err = io.Copy(w, f)
				return err
			}()
		})
	})

	// 批量下载指定文件和文件夹（打包为 zip）
	r.GET("/api/batch-download", authMiddleware, func(c *gin.Context) {
		paths := c.QueryArray("paths")
		if len(paths) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未提供下载路径"})
			return
		}

		c.Header("Content-Disposition", "attachment; filename=\"batch_download.zip\"")
		c.Header("Content-Type", "application/zip")

		zw := zip.NewWriter(c.Writer)
		defer zw.Close()

		baseDir := "./shared"

		for _, subPath := range paths {
			subPath = strings.ReplaceAll(subPath, "..", "")
			subPath = strings.Trim(subPath, "/\\")
			targetDir := filepath.Join(baseDir, subPath)

			info, err := os.Stat(targetDir)
			if err != nil {
				continue
			}

			if info.IsDir() {
				filepath.Walk(targetDir, func(path string, fInfo os.FileInfo, err error) error {
					if err != nil || fInfo.IsDir() {
						return nil
					}
					// 保持相对于当前下载项的目录结构
					relPath, err := filepath.Rel(targetDir, path)
					if err != nil {
						return nil
					}
					// 在 zip 内放在以该文件夹命名的目录下
					zipPath := filepath.Join(info.Name(), relPath)
					zipPath = strings.ReplaceAll(zipPath, "\\", "/")

					return func() error {
						f, err := os.Open(path)
						if err != nil {
							return err
						}
						defer f.Close()
						w, err := zw.Create(zipPath)
						if err != nil {
							return err
						}
						_, err = io.Copy(w, f)
						return err
					}()
				})
			} else {
				// 单个文件
				func() {
					f, err := os.Open(targetDir)
					if err != nil {
						return
					}
					defer f.Close()
					w, err := zw.Create(info.Name())
					if err != nil {
						return
					}
					io.Copy(w, f)
				}()
			}
		}
	})

	// 离线游戏静态资源
	os.MkdirAll("./games", os.ModePerm)
	r.Static("/games", "./games")

	// ====== 管理员接口 ======
	adminAuthMiddleware := func(c *gin.Context) {
		username := c.MustGet("username").(string)
		var user User
		if err := db.Where("username = ?", username).First(&user).Error; err != nil || (user.Role != "admin" && user.Role != "system") {
			c.JSON(http.StatusForbidden, gin.H{"error": "无管理员权限"})
			c.Abort()
			return
		}
		c.Set("role", user.Role)
		c.Set("system_level", user.SystemLevel)
		c.Next()
	}

	adminGroup := r.Group("/api/admin")
	adminGroup.Use(authMiddleware, adminAuthMiddleware)

	// 获取所有用户
	adminGroup.GET("/users", func(c *gin.Context) {
		var users []User
		db.Select("id", "created_at", "username", "avatar", "role", "is_muted", "is_banned", "can_play_games", "can_share_files", "system_level").Find(&users)
		c.JSON(http.StatusOK, users)
	})

	// 切换禁言状态
	adminGroup.POST("/mute", func(c *gin.Context) {
		var req struct {
			Username string `json:"username" binding:"required"`
			IsMuted  bool   `json:"is_muted"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}
		callerRole := c.MustGet("role").(string)
		callerLevel := c.MustGet("system_level").(int)

		var target User
		if err := db.Where("username = ?", req.Username).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		if callerRole == "admin" && target.Role != "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法操作同级或更高级别用户"})
			return
		}
		if callerRole == "system" && target.Role == "system" && callerLevel >= target.SystemLevel {
			if callerLevel != 1 { // 如果不是第一个创立体系的system
				c.JSON(http.StatusForbidden, gin.H{"error": "权限级别不足以操作同级或更高的 System 用户"})
				return
			} else if callerLevel == 1 && target.SystemLevel == 1 && target.Username != c.MustGet("username").(string) {
				// 主级system无法自己操作其他主级system，但一般只有一个主级
				c.JSON(http.StatusForbidden, gin.H{"error": "无法操作主 System 用户"})
				return
			}
		}

		if err := db.Model(&User{}).Where("username = ?", req.Username).Update("is_muted", req.IsMuted).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "操作成功"})
	})

	// 管理员强制删除共享文件/文件夹
	adminGroup.DELETE("/delete-shared", func(c *gin.Context) {
		subPath := c.Query("path")
		if subPath == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未提供路径"})
			return
		}

		// 防止路径穿越
		subPath = strings.ReplaceAll(subPath, "..", "")
		subPath = strings.Trim(subPath, "/\\")

		targetDir := filepath.Join("./shared", subPath)

		// 校验文件是否存在且位于 shared 目录
		absTarget, err := filepath.Abs(targetDir)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的路径"})
			return
		}

		absShared, _ := filepath.Abs("./shared")
		if !strings.HasPrefix(absTarget, absShared) {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法删除 shared 目录外的文件"})
			return
		}

		if err := os.RemoveAll(targetDir); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "文件(夹)已删除"})
	})

	// 切换封禁状态
	adminGroup.POST("/ban_user", func(c *gin.Context) {
		var req struct {
			Username string `json:"username" binding:"required"`
			IsBanned bool   `json:"is_banned"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}
		callerRole := c.MustGet("role").(string)
		callerLevel := c.MustGet("system_level").(int)

		var target User
		if err := db.Where("username = ?", req.Username).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		if callerRole == "admin" && target.Role != "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法操作同级或更高级别用户"})
			return
		}
		if callerRole == "system" && target.Role == "system" && callerLevel >= target.SystemLevel {
			if callerLevel != 1 {
				c.JSON(http.StatusForbidden, gin.H{"error": "权限级别不足以操作同级或更高的 System 用户"})
				return
			} else if callerLevel == 1 && target.SystemLevel == 1 && target.Username != c.MustGet("username").(string) {
				c.JSON(http.StatusForbidden, gin.H{"error": "无法操作同级主 System 用户"})
				return
			}
		}

		if err := db.Model(&User{}).Where("username = ?", req.Username).Update("is_banned", req.IsBanned).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
			return
		}

		if req.IsBanned {
			hub.disconnectByUsername(req.Username)
		}

		c.JSON(http.StatusOK, gin.H{"message": "操作成功"})
	})

	// 切换权限（游戏/文件）
	adminGroup.POST("/toggle_permission", func(c *gin.Context) {
		var req struct {
			Username   string `json:"username" binding:"required"`
			Permission string `json:"permission" binding:"required"` // can_play_games or can_share_files
			Value      bool   `json:"value"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		callerRole := c.MustGet("role").(string)
		callerLevel := c.MustGet("system_level").(int)

		var target User
		if err := db.Where("username = ?", req.Username).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		if callerRole == "admin" && target.Role != "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "无法操作同级或更高级别用户"})
			return
		}
		if callerRole == "system" && target.Role == "system" && callerLevel >= target.SystemLevel {
			if callerLevel != 1 {
				c.JSON(http.StatusForbidden, gin.H{"error": "权限级别不足以操作同级或更高的 System 用户"})
				return
			}
		}

		updateData := map[string]interface{}{}
		if req.Permission == "can_play_games" {
			updateData["can_play_games"] = req.Value
		} else if req.Permission == "can_share_files" {
			updateData["can_share_files"] = req.Value
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的权限名称"})
			return
		}

		if err := db.Model(&User{}).Where("username = ?", req.Username).Updates(updateData).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "操作成功"})
	})

	// 获取封禁 IP
	adminGroup.GET("/banned_ips", func(c *gin.Context) {
		var bans []IPBan
		db.Find(&bans)
		c.JSON(http.StatusOK, bans)
	})

	// 封禁/解封 IP
	adminGroup.POST("/ban_ip", func(c *gin.Context) {
		var req struct {
			IP     string `json:"ip" binding:"required"`
			Action string `json:"action" binding:"required"` // "ban", "unban"
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		if req.Action == "ban" {
			isRange := strings.Contains(req.IP, "/") || strings.Contains(req.IP, "-")
			db.Save(&IPBan{IP: req.IP, IsRange: isRange})
			if isRange {
				hub.disconnectBannedIPs() // 范围断开
			} else {
				hub.disconnectByIP(req.IP) // 单个断开
			}
		} else {
			// 解封操作
			// 只支持精确匹配解封，避免误删导致大范围放行
			if err := db.Where("ip = ?", req.IP).Delete(&IPBan{}).Error; err != nil {
				// ignore
			}
		}
		c.JSON(http.StatusOK, gin.H{"message": "操作成功"})
	})

	// 删除用户 (针对 User 或 Admin)
	adminGroup.DELETE("/users/:username", func(c *gin.Context) {
		targetUsername := c.Param("username")
		callerRole := c.MustGet("role").(string)
		callerLevel := c.MustGet("system_level").(int)

		var target User
		if err := db.Where("username = ?", targetUsername).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}

		if callerRole == "admin" && target.Role != "user" {
			c.JSON(http.StatusForbidden, gin.H{"error": "普通管理员仅能删除普通用户"})
			return
		}
		if target.Role == "system" {
			// 只有主级体系能够删除副级 System
			if callerLevel != 1 {
				c.JSON(http.StatusForbidden, gin.H{"error": "无法删除系统权限者"})
				return
			} else if target.SystemLevel == 1 {
				c.JSON(http.StatusForbidden, gin.H{"error": "不能删除同级的主系统权限者"})
				return
			}
		}

		// 使用 Unscoped 彻底删除，以修复后续无法再次注册同名用户的问题
		db.Unscoped().Where("username = ?", targetUsername).Delete(&User{})
		hub.disconnectByUsername(targetUsername)

		c.JSON(http.StatusOK, gin.H{"message": "用户删除成功"})
	})

	// 修改管理员密码
	adminGroup.POST("/password", func(c *gin.Context) {
		var req struct {
			NewPassword string `json:"new_password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}
		db.Save(&Config{Key: "admin_password", Value: req.NewPassword})
		c.JSON(http.StatusOK, gin.H{"message": "管理员密码修改成功"})
	})

	// ====== System 级接口 ======
	// 分配或取消 Role
	adminGroup.POST("/set_role", func(c *gin.Context) {
		callerRole := c.MustGet("role").(string)
		callerLevel := c.MustGet("system_level").(int)

		if callerRole != "system" {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有 system 角色可执行此操作"})
			return
		}
		var req struct {
			Username string `json:"username" binding:"required"`
			Role     string `json:"role" binding:"required"` // "system", "admin", "user"
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		var target User
		if err := db.Where("username = ?", req.Username).First(&target).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}

		if target.Role == "system" && req.Role != "system" {
			if callerLevel != 1 {
				c.JSON(http.StatusForbidden, gin.H{"error": "权限级别不足, 无法降级 System 的身份"})
				return
			} else if target.SystemLevel == 1 {
				c.JSON(http.StatusForbidden, gin.H{"error": "无法干涉主 System 的身份"})
				return
			}
		}

		updateData := map[string]interface{}{"role": req.Role}
		if req.Role == "system" && target.Role != "system" {
			// 分配的 system 为副 system (level 2)
			updateData["system_level"] = 2
		} else if req.Role != "system" {
			updateData["system_level"] = 0
		}

		if err := db.Model(&User{}).Where("username = ?", req.Username).Updates(updateData).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "角色修改失败"})
			return
		}

		// 通知对方在线客户端热更新 Role
		for client := range hub.clients {
			if client.Username == req.Username {
				client.Role = req.Role
				client.send <- Message{
					Type: "role_update",
					Role: req.Role,
				}
			}
		}

		c.JSON(http.StatusOK, gin.H{"message": "角色分配成功"})
	})

	// 修改系统管理员密码
	adminGroup.POST("/system_password", func(c *gin.Context) {
		callerRole := c.MustGet("role").(string)
		callerLevel := c.MustGet("system_level").(int)

		if callerRole != "system" || callerLevel != 1 {
			c.JSON(http.StatusForbidden, gin.H{"error": "只有主 system(level 1) 可执行此操作"})
			return
		}
		var req struct {
			NewPassword string `json:"new_password" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}
		db.Save(&Config{Key: "system_password", Value: req.NewPassword})
		c.JSON(http.StatusOK, gin.H{"message": "系统级密码修改成功"})
	})

	// ====== 审核相关接口 ======
	// 获取待审核列表
	adminGroup.GET("/pending_uploads", func(c *gin.Context) {
		var pending []PendingUpload
		db.Where("status = ?", "pending").Find(&pending)
		c.JSON(http.StatusOK, pending)
	})

	// 同意上传
	adminGroup.POST("/approve_upload", func(c *gin.Context) {
		var req struct {
			ID uint `json:"id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		var pending PendingUpload
		if err := db.First(&pending, req.ID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "不存在该审核记录"})
			return
		}

		if pending.Status != "pending" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "该记录已被处理"})
			return
		}

		destDir := fmt.Sprintf("./shared/%s_%s", pending.Username, pending.FolderName)

		// 将相对路径中的 / 统一为系统分隔符处理目标路径
		destDir = filepath.Clean(destDir)

		// 移动临时文件/文件夹到 shared 目录下
		if err := os.Rename(pending.TempPath, destDir); err != nil {
			// 如果在不同挂载点 Rename 可能会抛出 cross-device link 错误
			// 实现跨盘或 fallback 复制机制
			errCopy := filepath.Walk(pending.TempPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				relPath, err := filepath.Rel(pending.TempPath, path)
				if err != nil {
					return err
				}
				targetPath := filepath.Join(destDir, relPath)
				if info.IsDir() {
					return os.MkdirAll(targetPath, info.Mode())
				}
				srcFile, err := os.Open(path)
				if err != nil {
					return err
				}
				defer srcFile.Close()
				dstFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
				if err != nil {
					return err
				}
				defer dstFile.Close()
				_, err = io.Copy(dstFile, srcFile)
				return err
			})

			if errCopy != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "移动文件失败且复制回退也失败: " + errCopy.Error()})
				return
			}
			// 复制成功，删除原临时目录
			os.RemoveAll(pending.TempPath)
		}

		db.Model(&pending).Update("status", "approved")

		c.JSON(http.StatusOK, gin.H{"message": "审核通过"})
	})

	// 拒绝上传
	adminGroup.POST("/reject_upload", func(c *gin.Context) {
		var req struct {
			ID uint `json:"id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		var pending PendingUpload
		if err := db.First(&pending, req.ID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "不存在该审核记录"})
			return
		}

		if pending.Status != "pending" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "该记录已被处理"})
			return
		}

		// 删除临时文件
		os.RemoveAll(pending.TempPath)
		db.Model(&pending).Update("status", "rejected")

		c.JSON(http.StatusOK, gin.H{"message": "已拒绝该文件的分享"})
	})

	// 嵌入的前端静态资源
	subFS, _ := fs.Sub(frontendStatic, "dist")
	r.NoRoute(func(c *gin.Context) {
		// 如果是 API 请求但没找到路由，由 Gin 处理 (404)
		// 否则尝试从嵌入文件系统中读
		fileServer := http.FileServer(http.FS(subFS))
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	// 启动！！！！！
	fmt.Println("------------------------------")
	fmt.Println("🚀 AirChat 后端已成功启动！")
	fmt.Println("📍 监听端口: :8080")
	fmt.Println("🔗 WebSocket 入口: ws://127.0.0.1:8080/ws?token=YOUR_TOKEN")
	fmt.Println("------------------------------")

	r.Run(":8080")
}
