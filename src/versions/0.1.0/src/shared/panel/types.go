package panel

// 本文件定义面板接口的 DTO，字段与 design/schemas/panel/panel-dto.schema.json 一一对应。
// 事实来源：src/references/Xboard-master 对应 Controller 的 success() 返回值与 Resource。

// Credentials 是登录时提交的凭据。
//
// Password 只用于本次请求，禁止落盘或写入日志。
type Credentials struct {
	Email    string
	Password string
}

// Session 是登录成功后的会话与订阅凭据。
//
// 语义区分（重要，源码 app/Services/AuthService.php:18-36）：
//   - SessionToken 对应 auth_data，形如 "Bearer <token>"，用于 Authorization 头；
//   - SubscriptionToken 对应 token，是用户的订阅 token，用于获取订阅链接。
//
// 两者不可混用。SessionToken 有效期由服务端设定为一年，且没有 refresh 机制。
type Session struct {
	// SessionToken 是完整的 Authorization 头值，原样使用，不要自行拼接 "Bearer"。
	SessionToken string `json:"auth_data"`
	// SubscriptionToken 是订阅 token，属秘密。
	SubscriptionToken string `json:"token"`
	// IsAdmin 仅用于展示；客户端不访问任何管理员接口。
	IsAdmin bool `json:"is_admin"`
}

// Account 是账户信息（GET /api/v1/user/info）。
type Account struct {
	Email           string  `json:"email"`
	UUID            string  `json:"uuid"`
	TransferEnable  int64   `json:"transfer_enable"`
	UploadedBytes   int64   `json:"u"`
	DownloadedBytes int64   `json:"d"`
	// ExpiredAt 为 unix 秒；0 或缺失表示长期有效。
	ExpiredAt *int64 `json:"expired_at"`
	// Banned 服务端为数值，0 表示未封禁。
	Banned  int     `json:"banned"`
	PlanID  *int64  `json:"plan_id"`
	Balance float64 `json:"balance"`
	// AvatarURL 由服务端按 email 的 md5 生成。
	// 客户端不得自动请求它（会造成外联泄漏），仅在用户主动点击时加载。
	AvatarURL string `json:"avatar_url"`
}

// UsedBytes 返回已用流量总量（上传 + 下载）。
func (a Account) UsedBytes() int64 { return a.UploadedBytes + a.DownloadedBytes }

// RemainingBytes 返回剩余额度。当剩余为负时返回 0。
func (a Account) RemainingBytes() int64 {
	remaining := a.TransferEnable - a.UsedBytes()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IsExpired 判断账户是否已过期。ExpiredAt 为 nil 或 0 视为长期有效。
func (a Account) IsExpired(nowUnix int64) bool {
	if a.ExpiredAt == nil || *a.ExpiredAt == 0 {
		return false
	}
	return *a.ExpiredAt < nowUnix
}

// IsBanned 按布尔语义解释服务端的数值字段。
func (a Account) IsBanned() bool { return a.Banned != 0 }

// Plan 是套餐摘要。字段为服务端 Plan 模型的直接透传，未逐字段审计，按可选处理。
type Plan struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	TransferEnable *int64  `json:"transfer_enable"`
	SpeedLimit     *int64  `json:"speed_limit"`
	DeviceLimit    *int64  `json:"device_limit"`
	MonthlyPrice   float64 `json:"monthly_price"`
}

// SubscriptionDescriptor 是订阅描述（GET /api/v1/user/getSubscribe）。
//
// SubscribeURL 由服务端拼接，可能带查询参数，必须直接使用。
// 禁止自行拼接 "/api/v1/client/subscribe?token=..."，否则在部分部署下会失效。
type SubscriptionDescriptor struct {
	// SubscribeURL 是完整订阅链接，含 token，属秘密。
	SubscribeURL string `json:"subscribe_url"`
	// SubscriptionToken 与登录返回的 token 同源，属秘密。
	SubscriptionToken string `json:"token"`
	// UUID 是协议用户标识，属秘密。
	UUID            string `json:"uuid"`
	Email           string `json:"email"`
	PlanID          *int64 `json:"plan_id"`
	Plan            *Plan  `json:"plan"`
	ExpiredAt       *int64 `json:"expired_at"`
	UploadedBytes   int64  `json:"u"`
	DownloadedBytes int64  `json:"d"`
	TransferEnable  int64  `json:"transfer_enable"`
	DeviceLimit     *int64 `json:"device_limit"`
	SpeedLimit      *int64 `json:"speed_limit"`
	NextResetAt     *int64 `json:"next_reset_at"`
	ResetDay        *int64 `json:"reset_day"`
}

// UsedBytes 返回已用流量总量。
func (s SubscriptionDescriptor) UsedBytes() int64 {
	return s.UploadedBytes + s.DownloadedBytes
}

// PanelIdentity 是面板身份探测结果（GET /api/v1/guest/comm/config）。
//
// 该接口字段可被服务端插件增删，因此只保留原始映射，
// 不假设任何固定字段；需要具体字段时由调用方按键读取。
type PanelIdentity struct {
	// Raw 是原始响应 data 对象。
	Raw map[string]any
}
