package pgs

import (
	"fmt"
	"net/http"
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
// isSelfReferentialRedirect checks if a redirect rule would redirect to itself.
// This includes exact matches and wildcard patterns that would match the same path.
func isSelfReferentialRedirect(from, to string) bool {
	// External URLs are never self-referential
	if isUrl(to) {
		return false
	}

	// Exact match: /page redirects to /page
	if from == to {
		return true
	}

	// Wildcard match: /* redirects to /*
	if from == to && strings.Contains(from, "*") {
		return true
	}

	// Pattern with variable: /:path redirects to /:path
	if from == to && strings.Contains(from, ":") {
		return true
	}

	return false
}

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
			return rules, fmt.Errorf("missing destination path/URL")
		}

		from := parts[0]
		rest := parts[1:]
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
				if isToPart(part) {
					toIndex = idx
				}
			}

			if toIndex == -1 {
				return rules, fmt.Errorf("the destination path/URL must start with '/', 'http:' or 'https:'")
			}

			queryParts := rest[:toIndex]
			to := rest[toIndex]
			lastParts := rest[toIndex+1:]
			conditions := map[string]string{}
			sts := http.StatusMovedPermanently
			frcd := false
			if len(lastParts) > 0 {
				sts, frcd = hasStatusCode(lastParts[0])
			}
			if len(lastParts) > 1 {
				conditions = parsePairs(lastParts[1:])
			}

			// Validate that the redirect is not self-referential
			if isSelfReferentialRedirect(from, to) {
				return rules, fmt.Errorf("self-referential redirect: '%s' cannot redirect to itself", from)
			}

			rules = append(rules, &RedirectRule{
				To:         to,
				From:       from,
				Status:     sts,
				Force:      frcd,
				Query:      parsePairs(queryParts),
				Conditions: conditions,
			})
		}
	}

	return rules, nil
}
