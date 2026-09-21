package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func sha256Sum(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func joinReasons(rs []string) string { return strings.Join(rs, "；") }

// newEventID 在请求未携带 event_id 时生成服务端编号。
// 外部来源重传应携带稳定 event_id 以获得幂等。
func newEventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败属于环境性故障，直接 panic 由进程层处理。
		panic(err)
	}
	return "EV-" + hex.EncodeToString(b[:])
}
