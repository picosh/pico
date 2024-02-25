package pgs

import (
	"fmt"
	"slices"
	"strings"
)

type HeaderRule struct {
	Path    string
	Headers []*HeaderLine
}

type HeaderLine struct {
	Path  string
	Name  string
	Value string
}

var headerDenyList = []string{
	"accept-ranges",
	"age",
	"allow",
	"alt-svc",
	"connection",
	"content-encoding",
	"content-length",
	"content-range",
	"date",
	"location",
	"server",
	"trailer",
	"transfer-encoding",
	"upgrade",
}

func parseHeaderText(text string) ([]*HeaderRule, error) {
	rules := []*HeaderRule{}
	parsed := []*HeaderLine{}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		parsedLine, err := parseLine(strings.TrimSpace(line))
		if parsedLine == nil {
			continue
		}
		if err != nil {
			return rules, err
		}
		parsed = append(parsed, parsedLine)
	}

	var prevPath *HeaderRule
	for _, rule := range parsed {
		if rule.Path != "" {
			if prevPath != nil {
				if len(prevPath.Headers) > 0 {
					rules = append(rules, prevPath)
				}
			}

			prevPath = &HeaderRule{
				Path: rule.Path,
			}
		} else if prevPath != nil {
			// do not add headers in deny list
			if slices.Contains(headerDenyList, rule.Name) {
				continue
			}
			prevPath.Headers = append(
				prevPath.Headers,
				&HeaderLine{Name: rule.Name, Value: rule.Value},
			)
		}
	}

	// cleanup
	if prevPath != nil && len(prevPath.Headers) > 0 {
		rules = append(rules, prevPath)
	}

	return rules, nil
}

func parseLine(line string) (*HeaderLine, error) {
	rule := &HeaderLine{}

	if isPathLine(line) {
		rule.Path = line
		return rule, nil
	}

	if isEmpty(line) {
		return nil, nil
	}

	if isComment(line) {
		return nil, nil
	}

	if !strings.Contains(line, ":") {
		return nil, nil
	}

	results := strings.SplitN(line, ":", 2)
	name := strings.ToLower(strings.TrimSpace(results[0]))
	value := strings.TrimSpace(results[1])

	if name == "" {
		return nil, fmt.Errorf("header name cannot be empty")
	}

	if value == "" {
		return nil, fmt.Errorf("header value cannot be empty")
	}

	rule.Name = name
	rule.Value = value
	return rule, nil
}

func isComment(line string) bool {
	return strings.HasPrefix(line, "#")
}

func isEmpty(line string) bool {
	return line == ""
}

func isPathLine(line string) bool {
	return strings.HasPrefix(line, "/")
}
