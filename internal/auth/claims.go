package auth

import "github.com/golang-jwt/jwt/v5"

// Claims 是 site 签发的 Access Token 载荷。
// Subject 为用户 ID；Username 为登录名。模型请求不得携带该 Token。
type Claims struct {
	jwt.RegisteredClaims
	Username     string       `json:"username,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}
