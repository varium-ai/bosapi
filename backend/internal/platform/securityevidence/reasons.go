package securityevidence

// IsInjectionReason reports whether a stable rule reason belongs to the
// injection event schema. Detection logic for these reasons is supplied by the
// optional advanced detector; the public core only classifies event metadata.
func IsInjectionReason(reason string) bool {
	switch reason {
	case "prompt_injection", "system_override", "exfil_directive", "role_override",
		"hidden_instruction", "credential_solicitation", "markdown_link_exfil",
		"markdown_link_value_exfil", "markdown_link_follow_exfil",
		"system_prompt_disclosure", "credential_path_directive", "covert_action",
		"instruction_boundary":
		return true
	}
	return false
}
