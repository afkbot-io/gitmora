package runtimeconfig

import (
	"errors"
	"os"
)

// RegistrationEnabled requires an explicit boolean; malformed deployment policy never opens signup.
func RegistrationEnabled() (bool, error) {
	switch os.Getenv("WORKGRAPH_REGISTRATION_ENABLED") {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, errors.New("WORKGRAPH_REGISTRATION_ENABLED must be true or false")
	}
}
