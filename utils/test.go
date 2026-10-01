package utils

import (
	"os"
	"testing"
)

const COMPONENT = "mycomponent"

func SkipCI(t *testing.T) {
	if os.Getenv("CI") == "" {
		t.Skip("Skipping testing in CI environment")
	}
}
