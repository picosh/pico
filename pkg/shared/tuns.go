package shared

import (
	"fmt"
	"strings"
)

// ParseTunsTCP parses the tunnelID and tunsInstance to return a user friendly address
func ParseTunsTCP(tunnelID string, tunsInstance string) (string, error) {
	// example string:
	// tcp://10.0.0.89:33652,tcp6://[2603:c020:400a:d000:bd71:c59f:720c:484b]:33652
	splitData := strings.Split(tunnelID, ":")
	if len(splitData) < 3 {
		return "", fmt.Errorf("invalid tunnelID: %s", tunnelID)
	}

	port := splitData[len(splitData)-1]
	if port == "" {
		return "", fmt.Errorf("invalid tunnelID: %s", tunnelID)
	}

	return fmt.Sprintf("%s:%s", tunsInstance, port), nil
}
