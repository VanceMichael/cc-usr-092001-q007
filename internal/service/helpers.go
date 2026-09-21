package service

import "encoding/json"

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err) // 输入均为可控结构体，序列化失败属于程序错误
	}
	return raw
}

func decodePayload(raw []byte, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
