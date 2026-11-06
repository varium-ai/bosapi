package domain

import "errors"

var ErrInvalidSecurityPolicy = errors.New("invalid security policy")

const SettingKeyToolSecurityMode = "security.tool_call_mode"

// ToolSecurityPolicy controls tool checks across all organizations and transports.
type ToolSecurityPolicy struct {
	Mode string `json:"mode"`
}

func (policy ToolSecurityPolicy) Validate() error {
	if policy.Mode != "observe" && policy.Mode != "block" {
		return ErrInvalidSecurityPolicy
	}
	return nil
}
