package interfaces

import (
	"strings"

	"bosapi/backend/internal/platform/errorcode"
)

// gatewayErrorMessages contains user-facing English messages for gateway errors
// that need more context than the default error-code text provides.
var gatewayErrorMessages = map[errorcode.Code]string{
	errorcode.InvalidProvider: "Invalid provider. Please contact the platform administrator to check whether the provider assigned to this API key is valid.",
	errorcode.ModelNotAllowed: "Invalid model. Please contact the platform administrator to check whether this API key supports the requested model.",
}

func gatewayErrorMessage(code errorcode.Code) string {
	if message, ok := gatewayErrorMessages[code]; ok {
		return message
	}
	return strings.ReplaceAll(string(code), "_", " ")
}
