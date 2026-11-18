package domain

import "errors"

var ErrInvalidSecurityPolicy = errors.New("invalid security policy")

const SettingKeyToolSecurityMode = "security.tool_call_mode"

// ToolSecurityPolicy controls tool and content-security checks across all
// organizations and transports. disabled skips content inspection only.
type ToolSecurityPolicy struct {
	Mode string `json:"mode"`
}

func (policy ToolSecurityPolicy) Validate() error {
	if policy.Mode != "observe" && policy.Mode != "block" && policy.Mode != "disabled" {
		return ErrInvalidSecurityPolicy
	}
	return nil
}
