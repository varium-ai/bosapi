package domain

const SettingKeySecurityInspectionMode = "security.inspection_mode"

// SecurityInspectionPolicy controls the global security inspection pipeline.
// observe and block preserve the existing pipeline behavior; disabled skips it.
type SecurityInspectionPolicy struct {
	Mode string `json:"mode"`
}

func (policy SecurityInspectionPolicy) Validate() error {
	if policy.Mode != "observe" && policy.Mode != "block" && policy.Mode != "disabled" {
		return ErrInvalidSecurityRule
	}
	return nil
}
