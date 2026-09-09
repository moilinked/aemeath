package conversation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewID 生成服务端对话 ID。
func NewID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate conversation id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
