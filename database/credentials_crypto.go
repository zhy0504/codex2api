package database

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
)

const credentialsEncPrefix = "enc:v1:"

var sensitiveCredentialKeys = map[string]struct{}{
	"refresh_token": {},
	"access_token":  {},
	"id_token":      {},
}

type credentialCipher struct {
	aead cipher.AEAD
}

func newCredentialCipher(rawKey string) (*credentialCipher, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return nil, nil
	}

	keyHash := sha256.Sum256([]byte(rawKey))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return nil, fmt.Errorf("创建加密块失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建 AES-GCM 失败: %w", err)
	}
	return &credentialCipher{aead: aead}, nil
}

func (cc *credentialCipher) EncryptString(plaintext string) (string, error) {
	if cc == nil || plaintext == "" {
		return plaintext, nil
	}
	if strings.HasPrefix(plaintext, credentialsEncPrefix) {
		return plaintext, nil
	}

	nonce := make([]byte, cc.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成随机 nonce 失败: %w", err)
	}
	ciphertext := cc.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, ciphertext...)
	return credentialsEncPrefix + base64.StdEncoding.EncodeToString(payload), nil
}

func (cc *credentialCipher) DecryptString(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if !strings.HasPrefix(ciphertext, credentialsEncPrefix) {
		return ciphertext, nil
	}
	if cc == nil {
		return "", errors.New("检测到加密凭据，但未配置 CREDENTIALS_ENCRYPTION_KEY")
	}

	encoded := strings.TrimPrefix(ciphertext, credentialsEncPrefix)
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("解码加密凭据失败: %w", err)
	}
	nonceSize := cc.aead.NonceSize()
	if len(payload) <= nonceSize {
		return "", errors.New("加密凭据格式无效")
	}
	nonce := payload[:nonceSize]
	sealed := payload[nonceSize:]
	plain, err := cc.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("解密凭据失败: %w", err)
	}
	return string(plain), nil
}

func (db *DB) SetCredentialsEncryptionKey(rawKey string) error {
	cc, err := newCredentialCipher(rawKey)
	if err != nil {
		return err
	}
	db.credentialCipher = cc
	return nil
}

func (db *DB) CredentialsEncryptionEnabled() bool {
	return db != nil && db.credentialCipher != nil
}

func isSensitiveCredentialKey(key string) bool {
	_, ok := sensitiveCredentialKeys[key]
	return ok
}

func (db *DB) encryptCredentialMap(input map[string]interface{}) (map[string]interface{}, bool, error) {
	if input == nil {
		return map[string]interface{}{}, false, nil
	}

	output := make(map[string]interface{}, len(input))
	for k, v := range input {
		output[k] = v
	}

	if db == nil || db.credentialCipher == nil {
		return output, false, nil
	}

	changed := false
	for key, val := range output {
		if !isSensitiveCredentialKey(key) {
			continue
		}
		s, ok := val.(string)
		if !ok || s == "" {
			continue
		}
		encrypted, err := db.credentialCipher.EncryptString(s)
		if err != nil {
			return nil, false, fmt.Errorf("加密字段 %s 失败: %w", key, err)
		}
		if encrypted != s {
			changed = true
		}
		output[key] = encrypted
	}

	return output, changed, nil
}

func (db *DB) decryptCredentialString(value string) (string, error) {
	if !strings.HasPrefix(value, credentialsEncPrefix) {
		return value, nil
	}
	if db == nil || db.credentialCipher == nil {
		return "", errors.New("检测到加密凭据，但当前未配置密钥")
	}
	return db.credentialCipher.DecryptString(value)
}

func (db *DB) decryptCredentialMapInPlace(accountID int64, creds map[string]interface{}) {
	if creds == nil {
		return
	}
	for key, val := range creds {
		if !isSensitiveCredentialKey(key) {
			continue
		}
		s, ok := val.(string)
		if !ok || s == "" {
			continue
		}
		plain, err := db.decryptCredentialString(s)
		if err != nil {
			log.Printf("[账号 %d] 解密字段 %s 失败: %v", accountID, key, err)
			creds[key] = ""
			continue
		}
		creds[key] = plain
	}
}

func (db *DB) EncryptExistingCredentialSecrets(ctx context.Context) (int, error) {
	if db == nil || db.credentialCipher == nil {
		return 0, nil
	}

	type pendingUpdate struct {
		id   int64
		json []byte
	}
	var updates []pendingUpdate

	rows, err := db.conn.QueryContext(ctx, `SELECT id, credentials FROM accounts`)
	if err != nil {
		return 0, fmt.Errorf("读取账号凭据失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var credJSON []byte
		if err := rows.Scan(&id, &credJSON); err != nil {
			return 0, fmt.Errorf("扫描账号凭据失败: %w", err)
		}

		credMap := make(map[string]interface{})
		if err := json.Unmarshal(credJSON, &credMap); err != nil {
			log.Printf("[账号 %d] 跳过凭据迁移：JSON 解析失败: %v", id, err)
			continue
		}

		encryptedMap, changed, err := db.encryptCredentialMap(credMap)
		if err != nil {
			return 0, fmt.Errorf("账号 %d 凭据加密失败: %w", id, err)
		}
		if !changed {
			continue
		}

		payload, err := json.Marshal(encryptedMap)
		if err != nil {
			return 0, fmt.Errorf("账号 %d 序列化加密凭据失败: %w", id, err)
		}
		updates = append(updates, pendingUpdate{id: id, json: payload})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("遍历账号凭据失败: %w", err)
	}

	if len(updates) == 0 {
		return 0, nil
	}

	updated := 0
	for _, item := range updates {
		if db.isSQLite() {
			if _, err := db.conn.ExecContext(ctx,
				`UPDATE accounts SET credentials = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
				string(item.json), item.id,
			); err != nil {
				return updated, fmt.Errorf("更新账号 %d 的加密凭据失败: %w", item.id, err)
			}
		} else {
			if _, err := db.conn.ExecContext(ctx,
				`UPDATE accounts SET credentials = $1::jsonb, updated_at = NOW() WHERE id = $2`,
				item.json, item.id,
			); err != nil {
				return updated, fmt.Errorf("更新账号 %d 的加密凭据失败: %w", item.id, err)
			}
		}
		updated++
	}

	return updated, nil
}
