package rsyncsender

import (
	"io"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/pkg/rsync-receiver/rsyncwire"
)

type filterRuleList struct {
	Filters []*filterRule
}

// exclude.c:add_rule.
func (l *filterRuleList) addRule(fr *filterRule) {
	if strings.HasSuffix(fr.pattern, "/") {
		fr.flag |= filtruleDirectory
		fr.pattern = strings.TrimSuffix(fr.pattern, "/")
	}
	if strings.ContainsFunc(fr.pattern, func(r rune) bool {
		return r == '*' || r == '[' || r == '?'
	}) {
		fr.flag |= filtruleWild
	}
	l.Filters = append(l.Filters, fr)
}

// exclude.c:check_filter.
func (l *filterRuleList) matches(name string) bool {
	for _, fr := range l.Filters {
		if fr.matches(name) {
			return true
		}
	}
	return false
}

// exclude.c:recv_filter_list.
func RecvFilterList(c *rsyncwire.Conn) (*filterRuleList, error) {
	var l filterRuleList
	const exclusionListEnd = 0
	for {
		length, err := c.ReadInt32()
		if err != nil {
			return nil, err
		}
		if length == exclusionListEnd {
			break
		}
		line := make([]byte, length)
		if _, err := io.ReadFull(c.Reader, line); err != nil {
			return nil, err
		}
		fr, err := parseFilter(string(line))
		if err != nil {
			return nil, err
		}
		l.addRule(fr)
	}
	return &l, nil
}

const (
	filtruleInclude = 1 << iota
	filtruleClearList
	filtruleDirectory
	filtruleWild
)

type filterRule struct {
	flag    int
	pattern string
}

// exclude.c:rule_matches.
func (fr *filterRule) matches(name string) bool {
	if fr.flag&filtruleWild != 0 {
		panic("wildcard filter rules not yet implemented")
	}
	if !strings.ContainsRune(fr.pattern, '/') &&
		fr.flag&filtruleWild == 0 {
		name = filepath.Base(name)
	}
	return fr.pattern == name
}

// exclude.c:parse_filter_str / exclude.c:parse_rule_tok.
func parseFilter(line string) (*filterRule, error) {
	rule := new(filterRule)

	// We only support what rsync calls XFLG_OLD_PREFIXES
	if strings.HasPrefix(line, "- ") {
		// clear include flag
		rule.flag &= ^filtruleInclude
		line = strings.TrimPrefix(line, "- ")
	} else if strings.HasPrefix(line, "+ ") {
		// set include flag
		rule.flag |= filtruleInclude
		line = strings.TrimPrefix(line, "+ ")
	} else if strings.HasPrefix(line, "!") {
		// set clear_list flag
		rule.flag |= filtruleClearList
	}

	rule.pattern = line

	return rule, nil
}
