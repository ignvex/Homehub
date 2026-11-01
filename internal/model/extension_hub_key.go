package model

// ============================================================
// ExtensionHubKey — 宿主签名私钥
// ============================================================

// ExtensionHubKey 保存宿主对 Extension `/hub/*` 通道签名的 Ed25519 私钥。
//
// 为什么要进数据库：私钥原本只是一个文件路径。多个 HomeHub 实例共用
// 同一个 Extension 时，它们必须持有**同一把**私钥（Extension 只认一把
// 公钥，见 hub-integration.md §2.3），而让每个实例各自维护一份文件副本
// 就是在制造「哪份才是当前生效的」这个问题。存进共享数据库之后，
// 配置一次，所有实例自动一致。
//
// **明文存储，不加密。** 这是有意的，理由写清楚避免后来者误以为是疏漏：
//
//   - 加密需要一把密钥，而这把密钥只能放在同一台机器的 config.json 或
//     环境变量里。这不算防护，只是把问题挪一层——能读数据库的人通常也
//     能读配置文件。
//   - 真正起作用的边界是数据库的访问控制与备份加密，而不是列级加密。
//   - 与 balance_targets.api_key 保持一致：同一个库里不该一半加密一半不加密，
//     那只会让人误判风险等级。
//
// 对应的对策写在别处：这把私钥**永不通过 API 返回**（连掩码都只给指纹），
// 读取接口只回公钥与 SHA-256 指纹。
//
// 表名：extension_hub_keys
type ExtensionHubKey struct {
	BaseModel
	// PrivateKeyPEM 是 PKCS#8 PEM 原文。保留原文而不是只存种子：
	// 用户可能是从 openssl 直接粘贴过来的，原样存回去才能让人确认
	// 「库里这把就是当初那把」。
	PrivateKeyPEM string `gorm:"type:text;not null" json:"-"`
	// PublicKey 是 base64 编码的公钥。
	//
	// 冗余存储而不是每次从私钥推导：读取状态时只需要公钥，而推导一次
	// 就要把私钥解进内存。少碰一次私钥就少一次泄露面。
	PublicKey string `gorm:"size:64;not null" json:"public_key"`
}

// TableName 指定表名。
func (ExtensionHubKey) TableName() string {
	return "extension_hub_keys"
}
