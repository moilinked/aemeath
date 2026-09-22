package auth

// Capabilities 是 site 签发的 JWT 中的权限位。
// chat-agent 只使用 CanChat；CanManageSite / CanManageUsers 由 site 解释。
type Capabilities struct {
	Chat        bool `json:"chat"`
	ManageSite  bool `json:"manage_site"`
	ManageUsers bool `json:"manage_users"`
}

// CanChat 表示是否允许调用对话接口。
func (capabilities Capabilities) CanChat() bool {
	return capabilities.Chat
}

// CanManageSite 表示是否允许管理站点。chat-agent 不执行该检查。
func (capabilities Capabilities) CanManageSite() bool {
	return capabilities.ManageSite
}

// CanManageUsers 表示是否允许管理用户。chat-agent 不执行该检查。
func (capabilities Capabilities) CanManageUsers() bool {
	return capabilities.ManageUsers
}
