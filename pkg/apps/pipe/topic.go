package pipe

import (
	"fmt"
	"strings"
)

// toTopic scopes a topic to user by prefixing name.
func toTopic(userName, topic string) string {
	if strings.HasPrefix(topic, userName+"/") {
		return topic
	}
	return fmt.Sprintf("%s/%s", userName, topic)
}

func toPublicTopic(topic string) string {
	if strings.HasPrefix(topic, "public/") {
		return topic
	}
	return fmt.Sprintf("public/%s", topic)
}

// TopicResolveInput contains all inputs needed for topic resolution.
type TopicResolveInput struct {
	UserName           string
	Topic              string
	IsAdmin            bool
	IsPublic           bool
	AccessList         []string
	ExistingAccessList []string
	HasExistingAccess  bool
	IsAccessCreator    bool
	HasUserAccess      bool
}

// TopicResolveOutput contains the resolved topic name and any error.
type TopicResolveOutput struct {
	Name             string
	WithoutUser      string
	AccessDenied     bool
	GenerateNewTopic bool
}

// resolveTopic determines the final topic name based on user, flags, and access control.
func resolveTopic(input TopicResolveInput) TopicResolveOutput {
	var name string
	var withoutUser string

	if input.IsAdmin && strings.HasPrefix(input.Topic, "/") {
		name = strings.TrimPrefix(input.Topic, "/")
		return TopicResolveOutput{Name: name, WithoutUser: withoutUser}
	}

	name = toTopic(input.UserName, input.Topic)
	if input.IsPublic {
		name = toPublicTopic(input.Topic)
		withoutUser = name
	} else {
		withoutUser = input.Topic
	}

	if input.HasExistingAccess && len(input.ExistingAccessList) > 0 && !input.IsAdmin {
		if input.HasUserAccess || input.IsAccessCreator {
			name = withoutUser
		} else if !input.IsPublic {
			name = toTopic(input.UserName, withoutUser)
		} else {
			return TopicResolveOutput{
				Name:             name,
				WithoutUser:      withoutUser,
				AccessDenied:     true,
				GenerateNewTopic: true,
			}
		}
	}

	return TopicResolveOutput{Name: name, WithoutUser: withoutUser}
}
