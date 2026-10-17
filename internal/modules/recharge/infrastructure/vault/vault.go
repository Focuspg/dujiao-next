// Package vault 负责用户凭据与预检令牌的加密、解密，以及目标账号指纹计算。
// 主密钥来自配置文件或环境变量（recharge.vault_key），与业务数据库分开保存。
package vault

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	appcrypto "github.com/dujiao-next/internal/crypto"
)

// CurrentKeyVersion 当前密钥版本；轮换时递增并保留旧版本解密能力
const CurrentKeyVersion = 1

const minSecretLength = 16

// Secret 保险箱中的明文内容
type Secret struct {
	Mode           string `json:"mode"`
	Session        string `json:"session,omitempty"`
	AccessToken    string `json:"access_token,omitempty"`
	PreflightToken string `json:"preflight_token,omitempty"`
}

// String 脱敏，防止误打日志
func (s Secret) String() string { return "Secret{mode=" + s.Mode + ", redacted}" }

// GoString 脱敏
func (s Secret) GoString() string { return s.String() }

// Vault 加解密与指纹
type Vault struct {
	keys           map[int][]byte
	fingerprintKey []byte
}

// New 创建保险箱。vaultKey 与 fingerprintKey 都必须配置且不能相同。
func New(vaultKey, fingerprintKey string) (*Vault, error) {
	vaultKey = strings.TrimSpace(vaultKey)
	fingerprintKey = strings.TrimSpace(fingerprintKey)
	if len(vaultKey) < minSecretLength {
		return nil, errors.New("vault: recharge.vault_key must be at least 16 characters")
	}
	if len(fingerprintKey) < minSecretLength {
		return nil, errors.New("vault: recharge.fingerprint_key must be at least 16 characters")
	}
	if vaultKey == fingerprintKey {
		return nil, errors.New("vault: vault_key and fingerprint_key must differ")
	}
	return &Vault{
		keys:           map[int][]byte{CurrentKeyVersion: appcrypto.DeriveKey("recharge-vault:" + vaultKey)},
		fingerprintKey: []byte(fingerprintKey),
	}, nil
}

// Seal 加密，返回密文与密钥版本
func (v *Vault) Seal(secret Secret) (string, int, error) {
	raw, err := json.Marshal(secret)
	if err != nil {
		return "", 0, err
	}
	ct, err := appcrypto.Encrypt(v.keys[CurrentKeyVersion], string(raw))
	if err != nil {
		return "", 0, err
	}
	return ct, CurrentKeyVersion, nil
}

// Open 解密
func (v *Vault) Open(ciphertext string, keyVersion int) (Secret, error) {
	key, ok := v.keys[keyVersion]
	if !ok {
		return Secret{}, errors.New("vault: unknown key version")
	}
	plain, err := appcrypto.Decrypt(key, ciphertext)
	if err != nil {
		return Secret{}, errors.New("vault: decrypt failed")
	}
	var out Secret
	if err := json.Unmarshal([]byte(plain), &out); err != nil {
		return Secret{}, errors.New("vault: decode failed")
	}
	return out, nil
}

// Fingerprint 由稳定账号标识（产品 + 规范化邮箱/账号 ID）生成不可逆指纹。
// 不使用 Session 本身：Session 轮换后仍应识别为同一账号。
func (v *Vault) Fingerprint(product, accountID string) string {
	id := strings.ToLower(strings.TrimSpace(accountID))
	if id == "" {
		return ""
	}
	mac := hmac.New(sha256.New, v.fingerprintKey)
	mac.Write([]byte(strings.ToLower(strings.TrimSpace(product)) + "\x00" + id))
	return hex.EncodeToString(mac.Sum(nil))
}

// MaskAccount 脱敏账号标识：邮箱保留首字符和域名
func MaskAccount(accountID string) string {
	id := strings.TrimSpace(accountID)
	if id == "" {
		return ""
	}
	runes := []rune(id)
	at := strings.LastIndex(id, "@")
	if at > 0 {
		local := []rune(id[:at])
		return string(local[:1]) + "***" + id[at:]
	}
	if len(runes) <= 4 {
		return string(runes[:1]) + "***"
	}
	return string(runes[:2]) + "***" + string(runes[len(runes)-2:])
}
