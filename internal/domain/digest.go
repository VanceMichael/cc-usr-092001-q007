package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// PayloadDigest 计算任意 JSON 可序列化负载的 sha256 摘要。
// 先按固定规则重新编排（键排序），保证同一清单无论提交时键序如何，摘要一致。
func PayloadDigest(v any) (string, error) {
	canonical, err := canonicalJSON(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// DigestBytes 计算原始字节的 sha256 摘要。
func DigestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// canonicalJSON 将负载编为规范化 JSON：Unmarshal 到 any 后再 Marshal，
// encoding/json 会按字典序输出 map 键。
func canonicalJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	return json.Marshal(generic)
}
