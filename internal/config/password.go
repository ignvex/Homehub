package config

import (
	"encoding/json"
	"log"
	"os"
)

// ClearPassword 覆写 config.json 中的 auth.password 为空字符串。
//
// 首次创建用户后调用，防止明文密码永久残留在配置文件中。
// 失败时不阻塞启动流程，仅记录警告。
func ClearPassword(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[config] ClearPassword: 读取配置文件失败: %v", err)
		return
	}

	// 使用 map 避免依赖 Config 结构体的字段顺序
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Printf("[config] ClearPassword: 解析配置文件失败: %v", err)
		return
	}

	auth, ok := raw["auth"].(map[string]any)
	if !ok {
		return
	}

	if _, hasPassword := auth["password"]; !hasPassword {
		return
	}

	auth["password"] = ""

	// 写回（保留缩进格式）
	out, err := json.MarshalIndent(raw, "", "    ")
	if err != nil {
		log.Printf("[config] ClearPassword: 序列化配置失败: %v", err)
		return
	}

	if err := os.WriteFile(path, append(out, '\n'), 0644); err != nil {
		log.Printf("[config] ClearPassword: 写入配置文件失败: %v", err)
		return
	}

	log.Printf("[config] 已清除 auth.password（初始用户已创建）")
}
