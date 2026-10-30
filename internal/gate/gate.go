package gate

// users 硬编码门控映射：输入的 key → (userID, heroineName)
// userID 与数据库 users.id 一致，也用作 cookie 值
var users = map[string]string{
	"hui": "夏晚棠",
	"wen": "沈晚",
}

// Validate 检验 key 是否合法，返回对应的 userID 和 heroineName。
func Validate(key string) (userID, heroineName string, ok bool) {
	name, ok := users[key]
	if !ok {
		return "", "", false
	}
	return key, name, true
}
