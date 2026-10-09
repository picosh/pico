package rsync

import (
	"fmt"
	"strings"
)

// Filter rule flags (rsync.h FILTRULE_*).
const (
	ruleWild = 1 << iota
	ruleWild2
	ruleWild2Prefix
	ruleWild3Suffix
	ruleAbsPath
	ruleInclude
	ruleDirectory
	ruleWordSplit
	ruleNoInherit
	ruleNoPrefixes
	ruleMergeFile
	rulePerdirMerge
	ruleExcludeSelf
	ruleFinishSetup
	ruleNegate
	ruleCVSIgnore
	ruleSenderSide
	ruleReceiverSide
	ruleClearList
	rulePerishable
	ruleXattr
)

type filterRule struct {
	pattern  string
	flags    int
	slashCnt int
}

// filterList holds the rules the client sent. The client only sends the
// rules that apply to our side of the transfer.
type filterList struct {
	rules []*filterRule
}

// defaultCVSIgnore is the list rsync's -C adds.
const defaultCVSIgnore = "RCS SCCS CVS CVS.adm RCSLOG cvslog.* tags TAGS" +
	" .make.state .nse_depinfo *~ #* .#* ,* _$* *$" +
	" *.old *.bak *.BAK *.orig *.rej .del-*" +
	" *.a *.olb *.o *.obj *.so *.exe" +
	" *.Z *.elc *.ln core" +
	" .svn/ .git/ .hg/ .bzr/"

// parse adds the rules in one line received from the client. Before
// protocol 29 only the "- " and "+ " prefixes exist.
func (l *filterList) parse(line string, oldPrefixes bool) error {
	rule := &filterRule{}
	s := line
	switch {
	case oldPrefixes:
		switch {
		case strings.HasPrefix(s, "- "):
			s = s[2:]
		case strings.HasPrefix(s, "+ "):
			rule.flags |= ruleInclude
			s = s[2:]
		case strings.HasPrefix(s, "!"):
			rule.flags |= ruleClearList
		}
	default:
		// Clients send the short form built by get_rule_prefix: a rule
		// character, optional modifiers, then a space or underscore.
		if s == "" {
			return nil
		}
		ch := s[0]
		i := 1
		if len(s) > 1 && s[1] == ',' {
			i = 2
		}
		sideFromPrefix := false
		switch ch {
		case ':':
			rule.flags |= rulePerdirMerge | ruleFinishSetup | ruleMergeFile
		case '.':
			rule.flags |= ruleMergeFile
		case '+':
			rule.flags |= ruleInclude
		case '-':
		case 'S':
			rule.flags |= ruleInclude | ruleSenderSide
			sideFromPrefix = true
		case 'H':
			rule.flags |= ruleSenderSide
			sideFromPrefix = true
		case 'R':
			rule.flags |= ruleInclude | ruleReceiverSide
			sideFromPrefix = true
		case 'P':
			rule.flags |= ruleReceiverSide
			sideFromPrefix = true
		case '!':
			rule.flags |= ruleClearList
		default:
			return fmt.Errorf("unknown filter rule: %q", line)
		}
		if ch != '!' {
			for ; i < len(s) && s[i] != ' ' && s[i] != '_'; i++ {
				switch s[i] {
				case '-':
					rule.flags |= ruleNoPrefixes
				case '+':
					rule.flags |= ruleNoPrefixes | ruleInclude
				case '/':
					rule.flags |= ruleAbsPath
				case '!':
					rule.flags |= ruleNegate
				case 'C':
					if sideFromPrefix {
						return fmt.Errorf("invalid modifier in filter rule: %q", line)
					}
					rule.flags |= ruleNoPrefixes | ruleWordSplit | ruleNoInherit | ruleCVSIgnore
				case 'e':
					rule.flags |= ruleExcludeSelf
				case 'n':
					rule.flags |= ruleNoInherit
				case 'p':
					rule.flags |= rulePerishable
				case 'r':
					rule.flags |= ruleReceiverSide
				case 's':
					rule.flags |= ruleSenderSide
				case 'w':
					rule.flags |= ruleWordSplit
				case 'x':
					rule.flags |= ruleXattr
				default:
					return fmt.Errorf("invalid modifier in filter rule: %q", line)
				}
			}
			if i < len(s) {
				i++
			}
		}
		s = s[min(i, len(s)):]
	}

	if rule.flags&ruleClearList != 0 {
		if len(s) <= 1 {
			l.rules = nil
			return nil
		}
		rule.flags &^= ruleClearList
	}

	switch {
	case rule.flags&ruleMergeFile != 0:
		// Merge files would have to be read from storage on our side.
		// They can only add rules for files we don't have, so skip them.
		return nil
	case rule.flags&ruleCVSIgnore != 0:
		for _, pat := range strings.Fields(defaultCVSIgnore) {
			l.add(pat, ruleNoPrefixes)
		}
		return nil
	case s == "":
		return fmt.Errorf("unexpected end of filter rule: %q", line)
	}
	l.add(s, rule.flags)
	return nil
}

func (l *filterList) add(pat string, flags int) {
	rule := &filterRule{flags: flags}
	if len(pat) > 1 && strings.HasSuffix(pat, "/") {
		pat = pat[:len(pat)-1]
		rule.flags |= ruleDirectory
	}
	rule.slashCnt = strings.Count(pat, "/")
	rule.pattern = pat
	if strings.ContainsAny(pat, "*[?") {
		rule.flags |= ruleWild
		if i := strings.Index(pat, "**"); i >= 0 {
			rule.flags |= ruleWild2
			if i == 0 {
				rule.flags |= ruleWild2Prefix
			}
			if strings.HasSuffix(pat, "***") {
				rule.flags |= ruleWild3Suffix
			}
		}
	}
	l.rules = append(l.rules, rule)
}

// check returns -1 if name is excluded, 1 if it is included and 0 when no
// rule matched. name is relative to the transfer root.
func (l *filterList) check(name string, isDir bool) int {
	for _, r := range l.rules {
		if r.matches(name, isDir) {
			if r.flags&ruleInclude != 0 {
				return 1
			}
			return -1
		}
	}
	return 0
}

func (l *filterList) excluded(name string, isDir bool) bool {
	return l != nil && l.check(name, isDir) < 0
}

// matches is rsync's rule_matches.
func (r *filterRule) matches(fname string, isDir bool) bool {
	ret := r.flags&ruleNegate == 0
	name := strings.TrimPrefix(fname, "/")
	if name == "" || r.flags&ruleXattr != 0 {
		return false
	}

	var strs []string
	switch {
	case r.slashCnt == 0 && r.flags&ruleWild2 == 0:
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
	case r.flags&ruleWild2Prefix != 0 && !strings.HasPrefix(fname, "/"):
		strs = append(strs, "/")
	}
	strs = append(strs, name)
	if isDir {
		if r.flags&ruleWild3Suffix != 0 {
			strs = append(strs, "/")
		}
	} else if r.flags&ruleDirectory != 0 {
		return !ret
	}

	pattern := r.pattern
	anchored := false
	if strings.HasPrefix(pattern, "/") {
		anchored = true
		pattern = pattern[1:]
	}

	var where int
	switch {
	case !anchored && r.slashCnt > 0 && r.flags&ruleWild2 == 0:
		where = r.slashCnt + 1
	case !anchored && r.flags&ruleWild2Prefix == 0 && r.flags&ruleWild2 != 0:
		where = -1
	}

	text := strings.Join(strs, "")
	switch {
	case r.flags&ruleWild != 0:
		if wildmatchWhere(pattern, text, where) {
			return ret
		}
	case len(strs) > 1:
		if t, ok := trailingElements(text, where); ok && t == pattern {
			return ret
		}
	case anchored:
		if name == pattern {
			return ret
		}
	default:
		if strings.HasSuffix(name, pattern) &&
			(len(name) == len(pattern) || name[len(name)-len(pattern)-1] == '/') {
			return ret
		}
	}
	return !ret
}

// trailingElements returns the last count slash-separated elements of s.
// A count of zero or less returns s unchanged.
func trailingElements(s string, count int) (string, bool) {
	if count <= 0 {
		return s, true
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			count--
			if count == 0 {
				return s[i+1:], true
			}
		}
	}
	if count == 1 {
		return s, true
	}
	return "", false
}

// wildmatchWhere matches pattern against the trailing where elements of
// text (where > 0), at the start of text (where == 0), or at the start or
// after any slash (where < 0).
func wildmatchWhere(pattern, text string, where int) bool {
	t, ok := trailingElements(text, where)
	if !ok {
		return false
	}
	m := dowild(pattern, t)
	if m == wildMatch || where >= 0 || m == wildAbortAll {
		return m == wildMatch
	}
	for i := 0; i < len(t); i++ {
		if t[i] != '/' {
			continue
		}
		m = dowild(pattern, t[i+1:])
		if m != wildNoMatch && m != wildAbortToStarStar {
			return m == wildMatch
		}
	}
	return false
}

// wildmatch reports whether text matches the rsync wildcard pattern. "*"
// and "?" don't match "/", "**" does.
func wildmatch(pattern, text string) bool {
	return dowild(pattern, text) == wildMatch
}

const (
	wildNoMatch = iota
	wildMatch
	wildAbortAll
	wildAbortToStarStar
)

func dowild(p, text string) int {
	pi, ti := 0, 0
	for ; pi < len(p); pi, ti = pi+1, ti+1 {
		pch := p[pi]
		if ti >= len(text) && pch != '*' {
			return wildAbortAll
		}
		var tch byte
		if ti < len(text) {
			tch = text[ti]
		}
		switch pch {
		case '\\':
			pi++
			if pi >= len(p) || tch != p[pi] {
				return wildNoMatch
			}
		case '?':
			if tch == '/' {
				return wildNoMatch
			}
		case '*':
			special := false
			pi++
			if pi < len(p) && p[pi] == '*' {
				for pi < len(p) && p[pi] == '*' {
					pi++
				}
				special = true
			}
			if pi >= len(p) {
				// A trailing "**" matches everything, "*" only if
				// there are no more slashes.
				if !special && strings.IndexByte(text[ti:], '/') >= 0 {
					return wildNoMatch
				}
				return wildMatch
			}
			for ; ; ti++ {
				if ti >= len(text) {
					return wildAbortAll
				}
				m := dowild(p[pi:], text[ti:])
				if m != wildNoMatch {
					if !special || m != wildAbortToStarStar {
						return m
					}
				} else if !special && text[ti] == '/' {
					return wildAbortToStarStar
				}
			}
		case '[':
			pi++
			if pi >= len(p) {
				return wildAbortAll
			}
			negate := false
			if p[pi] == '!' || p[pi] == '^' {
				negate = true
				pi++
			}
			matched := false
			var prev byte
			for first := true; ; first = false {
				if pi >= len(p) {
					return wildAbortAll
				}
				c := p[pi]
				if c == ']' && !first {
					break
				}
				switch {
				case c == '\\':
					pi++
					if pi >= len(p) {
						return wildAbortAll
					}
					c = p[pi]
					if tch == c {
						matched = true
					}
				case c == '-' && prev != 0 && pi+1 < len(p) && p[pi+1] != ']':
					pi++
					c = p[pi]
					if c == '\\' {
						pi++
						if pi >= len(p) {
							return wildAbortAll
						}
						c = p[pi]
					}
					if tch <= c && tch >= prev {
						matched = true
					}
					c = 0
				case c == '[' && pi+1 < len(p) && p[pi+1] == ':':
					k := strings.IndexByte(p[pi+2:], ']')
					if k < 0 {
						return wildAbortAll
					}
					if k == 0 || p[pi+2+k-1] != ':' {
						// No ":]", so the '[' is literal.
						if tch == '[' {
							matched = true
						}
						break
					}
					ok, valid := charClass(p[pi+2:pi+2+k-1], tch)
					if !valid {
						return wildAbortAll
					}
					if ok {
						matched = true
					}
					pi += 2 + k
					c = 0
				default:
					if tch == c {
						matched = true
					}
				}
				prev = c
				pi++
			}
			if matched == negate || tch == '/' {
				return wildNoMatch
			}
		default:
			if tch != pch {
				return wildNoMatch
			}
		}
	}
	if ti < len(text) {
		return wildNoMatch
	}
	return wildMatch
}

func charClass(class string, c byte) (match, valid bool) {
	isUpper := c >= 'A' && c <= 'Z'
	isLower := c >= 'a' && c <= 'z'
	isDigit := c >= '0' && c <= '9'
	isAlpha := isUpper || isLower
	isSpace := c == ' ' || (c >= '\t' && c <= '\r')
	isPrint := c >= 0x20 && c < 0x7f
	switch class {
	case "alnum":
		return isAlpha || isDigit, true
	case "alpha":
		return isAlpha, true
	case "blank":
		return c == ' ' || c == '\t', true
	case "cntrl":
		return c < 0x20 || c == 0x7f, true
	case "digit":
		return isDigit, true
	case "graph":
		return isPrint && c != ' ', true
	case "lower":
		return isLower, true
	case "print":
		return isPrint, true
	case "punct":
		return isPrint && c != ' ' && !isAlpha && !isDigit, true
	case "space":
		return isSpace, true
	case "upper":
		return isUpper, true
	case "xdigit":
		return isDigit || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'), true
	}
	return false, false
}
