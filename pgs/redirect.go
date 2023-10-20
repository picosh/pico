package pgs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type RedirectRule struct {
	From       string
	To         string
	Status     int
	Query      map[string]string
	Conditions map[string]string
	Force      bool
	Signed     bool
}

var reSplitWhitespace = regexp.MustCompile(`\s+`)

func isUrl(text string) bool {
	return strings.HasPrefix(text, "http://") || strings.HasPrefix(text, "https://")
}

func isToPart(part string) bool {
	return strings.HasPrefix(part, "/") || isUrl(part)
}

func hasStatusCode(part string) (int, bool) {
	status := 0
	forced := false
	pt := part
	if strings.HasSuffix(part, "!") {
		pt = strings.TrimSuffix(part, "!")
		forced = true
	}

	status, err := strconv.Atoi(pt)
	if err != nil {
		return 0, forced
	}
	return status, forced
}

func parsePairs(pairs []string) map[string]string {
	mapper := map[string]string{}
	for _, pair := range pairs {
		val := strings.SplitN(pair, "=", 1)
		if len(val) > 1 {
			mapper[val[0]] = val[1]
		}
	}
	return mapper
}

/*
https://github.com/netlify/build/blob/main/packages/redirect-parser/src/line_parser.js#L9-L26
Parse `_redirects` file to an array of objects.
Each line in that file must be either:
  - An empty line
  - A comment starting with #
  - A redirect line, optionally ended with a comment

Each redirect line has the following format:

	from [query] [to] [status[!]] [conditions]

The parts are:
  - "from": a path or a URL
  - "query": a whitespace-separated list of "key=value"
  - "to": a path or a URL
  - "status": an HTTP status integer
  - "!": an optional exclamation mark appended to "status" meant to indicate
    "forced"
  - "conditions": a whitespace-separated list of "key=value"
  - "Sign" is a special condition
*/
func parseRedirectText(text string) ([]*RedirectRule, error) {
	rules := []*RedirectRule{}
	origLines := strings.Split(text, "\n")
	for _, line := range origLines {
		trimmed := strings.TrimSpace(line)
		// ignore empty lines
		if trimmed == "" {
			continue
		}

		// ignore comments
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		parts := reSplitWhitespace.Split(trimmed, -1)
		if len(parts) < 2 {
			return rules, fmt.Errorf("Missing destination path/URL")
		}

		from := parts[0]
		rest := parts[0:]
		status, forced := hasStatusCode(rest[0])
		if status != 0 {
			rules = append(rules, &RedirectRule{
				Query:  map[string]string{},
				Status: status,
				Force:  forced,
			})
		} else {
			toIndex := -1
			for idx, part := range rest {
				fmt.Println(part)
				if isToPart(part) {
					toIndex = idx
				}
			}

			if toIndex == -1 {
				return rules, fmt.Errorf("The destination path/URL must start with '/', 'http:' or 'https:'")
			}

			queryParts := parts[:toIndex]
			to := parts[toIndex]
			lastParts := parts[toIndex+1:]
			sts, frcd := hasStatusCode(lastParts[0])

			rules = append(rules, &RedirectRule{
				To:         to,
				From:       from,
				Status:     sts,
				Force:      frcd,
				Query:      parsePairs(queryParts),
				Conditions: parsePairs(lastParts[1:]),
			})
		}
	}

	return rules, nil
}
