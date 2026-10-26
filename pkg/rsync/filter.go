package rsync

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unsafe"
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

// containerFlags are the flags rules in a merge file take from the merge
// rule (FILTRULES_FROM_CONTAINER).
const containerFlags = ruleAbsPath | ruleInclude | ruleDirectory | ruleNegate | rulePerishable

const sideFlags = ruleSenderSide | ruleReceiverSide

// maxMergeFileSize bounds a per-directory merge file read from storage.
const maxMergeFileSize = 1 << 20

type filterRule struct {
	pattern  string
	flags    int
	slashCnt int
	// merge indexes filterList.merges for a per-directory merge rule.
	merge int
}

// filterList holds the rules the client sent. The client only sends the
// rules that apply to our side of the transfer. A per-directory merge rule
// stands for the rules read from that file in each directory, which a
// filterScope holds.
type filterList struct {
	rules  []*filterRule
	merges []*filterRule
	// otherSide marks merge-file rules that only apply to the client.
	otherSide      int
	deleteExcluded bool
	cvsPerishable  bool
	mergeBytes     int
	mergeRules     int
	scopeBytes     int
}

// filterScope is the per-directory merge-file rules in effect in one
// directory, each list holding the directory's own rules before the
// inherited ones.
type filterScope struct {
	// root is the storage path of the transfer root, which absolute
	// patterns are matched from.
	root  string
	lists [][]*filterRule
}

// mergeFileError is a per-directory merge file that could not be used.
// Skipping its rules could delete or send files they cover, so callers
// stop instead.
type mergeFileError struct {
	name string
	err  error
}

func (e *mergeFileError) Error() string {
	return fmt.Sprintf("per-directory filter file %q: %v", "/"+e.name, e.err)
}

func (e *mergeFileError) Unwrap() error { return e.err }

// defaultCVSIgnore is the list rsync's -C adds.
const defaultCVSIgnore = "RCS SCCS CVS CVS.adm RCSLOG cvslog.* tags TAGS" +
	" .make.state .nse_depinfo *~ #* .#* ,* _$* *$" +
	" *.old *.bak *.BAK *.orig *.rej .del-*" +
	" *.a *.olb *.o *.obj *.so *.exe" +
	" *.Z *.elc *.ln core" +
	" .svn/ .git/ .hg/ .bzr/"

var longRuleNames = map[byte]struct {
	word string
	ch   byte
}{
	'c': {"clear", '!'},
	'd': {"dir-merge", ':'},
	'e': {"exclude", '-'},
	'h': {"hide", 'H'},
	'i': {"include", '+'},
	'm': {"merge", '.'},
	'p': {"protect", 'P'},
	'r': {"risk", 'R'},
	's': {"show", 'S'},
}

func isSpace(c byte) bool { return c == ' ' || (c >= '\t' && c <= '\r') }

// parseRuleTok parses one rule (parse_rule_tok). tmpl is the flags of the
// merge rule whose file the rule was read from, or 0 for the client's
// rules. It returns a nil rule for an empty one.
func parseRuleTok(s string, tmpl int, oldPrefixes bool) (*filterRule, error) {
	if tmpl&ruleWordSplit != 0 {
		s = strings.TrimLeftFunc(s, func(r rune) bool { return r < 0x80 && isSpace(byte(r)) })
	}
	if s == "" {
		return nil, nil
	}
	rule := &filterRule{flags: tmpl & containerFlags}
	switch {
	case tmpl&ruleNoPrefixes != 0:
		if s[0] == '!' && tmpl&ruleCVSIgnore != 0 {
			rule.flags |= ruleClearList
		}
	case oldPrefixes:
		switch {
		case strings.HasPrefix(s, "- "):
			rule.flags &^= ruleInclude
			s = s[2:]
		case strings.HasPrefix(s, "+ "):
			rule.flags |= ruleInclude
			s = s[2:]
		case s[0] == '!':
			rule.flags |= ruleClearList
		}
	default:
		// p ends on the last character of the rule name, as the modifier
		// loop starts by stepping past it.
		ch, p := s[0], 0
		if long, ok := longRuleNames[s[0]]; ok {
			rest, found := strings.CutPrefix(s, long.word)
			switch {
			case !found:
				return nil, errors.New("unknown filter rule")
			case rest == "" || rest[0] == '_' || isSpace(rest[0]):
				ch, p = long.ch, len(long.word)-1
			case rest[0] == ',':
				ch, p = long.ch, len(long.word)
			default:
				return nil, errors.New("unknown filter rule")
			}
		} else if len(s) > 1 && s[1] == ',' {
			p = 1
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
			return nil, errors.New("unknown filter rule")
		}
		merge := rule.flags&ruleMergeFile != 0
		for ch != '!' {
			p++
			if p >= len(s) || s[p] == ' ' || s[p] == '_' {
				break
			}
			if tmpl&ruleWordSplit != 0 && isSpace(s[p]) {
				p--
				break
			}
			valid := true
			switch s[p] {
			case '-':
				valid = merge && rule.flags&ruleNoPrefixes == 0
				rule.flags |= ruleNoPrefixes
			case '+':
				valid = merge && rule.flags&ruleNoPrefixes == 0
				rule.flags |= ruleNoPrefixes | ruleInclude
			case '/':
				rule.flags |= ruleAbsPath
			case '!':
				valid = !merge
				rule.flags |= ruleNegate
			case 'C':
				valid = rule.flags&ruleNoPrefixes == 0 && !sideFromPrefix
				rule.flags |= ruleNoPrefixes | ruleWordSplit | ruleNoInherit | ruleCVSIgnore
			case 'e':
				valid = merge
				rule.flags |= ruleExcludeSelf
			case 'n':
				valid = merge
				rule.flags |= ruleNoInherit
			case 'p':
				rule.flags |= rulePerishable
			case 'r':
				valid = !sideFromPrefix
				rule.flags |= ruleReceiverSide
			case 's':
				valid = !sideFromPrefix
				rule.flags |= ruleSenderSide
			case 'w':
				valid = merge
				rule.flags |= ruleWordSplit
			case 'x':
				rule.flags |= ruleXattr
			default:
				valid = false
			}
			if !valid {
				return nil, fmt.Errorf("invalid modifier %q in filter rule", s[p])
			}
		}
		if p < len(s) {
			p++
		}
		s = s[min(p, len(s)):]
	}
	if tmpl&sideFlags != 0 {
		if rule.flags&sideFlags != 0 {
			return nil, errors.New("specified-side merge file contains specified-side filter")
		}
		rule.flags |= tmpl & sideFlags
	}

	if tmpl&ruleWordSplit != 0 {
		if i := strings.IndexFunc(s, func(r rune) bool { return r < 0x80 && isSpace(byte(r)) }); i >= 0 {
			s = s[:i]
		}
	}
	if rule.flags&ruleClearList != 0 {
		if tmpl&ruleNoPrefixes == 0 && !oldPrefixes && s != "" {
			return nil, errors.New("'!' rule has trailing characters")
		}
		if len(s) > 1 {
			rule.flags &^= ruleClearList
		}
	} else if s == "" && rule.flags&ruleCVSIgnore == 0 {
		return nil, errors.New("unexpected end of filter rule")
	}
	if len(s) >= maxPathLen {
		return nil, errors.New("filter rule too long")
	}
	rule.pattern = s
	return rule, nil
}

// parse adds the rules in one line received from the client. Before
// protocol 29 only the "- " and "+ " prefixes exist.
func (l *filterList) parse(line string, oldPrefixes bool) error {
	rule, err := parseRuleTok(line, 0, oldPrefixes)
	switch {
	case err != nil:
		return fmt.Errorf("%w: %q", err, line)
	case rule == nil:
		return nil
	case rule.flags&ruleClearList != 0:
		l.rules, l.merges = nil, nil
		return nil
	case rule.flags&ruleMergeFile != 0:
		if rule.flags&rulePerdirMerge == 0 {
			// Clients read merge files themselves and send their rules.
			return fmt.Errorf("merge-file filter rules are not supported: %q", line)
		}
		return l.addMerge(rule)
	case rule.flags&ruleCVSIgnore != 0:
		l.rules = append(l.rules, l.cvsIgnoreRules()...)
		return nil
	}
	l.add(rule.pattern, rule.flags)
	return nil
}

// addMerge adds a per-directory merge rule. Only the file's base name is
// read in each directory; a directory part names where the scan of parent
// directories starts (setup_merge_file).
func (l *filterList) addMerge(rule *filterRule) error {
	if rule.pattern == "" {
		rule.pattern = ".cvsignore"
	}
	base := path.Base(rule.pattern)
	if strings.HasSuffix(rule.pattern, "/") || base == "." || base == ".." {
		return fmt.Errorf("invalid merge-file name: %q", rule.pattern)
	}
	if rule.flags&ruleExcludeSelf != 0 {
		l.add(base, 0)
		rule.flags &^= ruleExcludeSelf
	}
	for _, m := range l.merges {
		if path.Base(m.pattern) == base {
			return nil
		}
	}
	rule.merge = len(l.merges)
	l.rules = append(l.rules, rule)
	l.merges = append(l.merges, rule)
	return nil
}

// cvsIgnoreRules returns rsync's default CVS excludes, which are
// perishable from protocol 30 (get_cvs_excludes).
func (l *filterList) cvsIgnoreRules() []*filterRule {
	flags := ruleNoPrefixes
	if l.cvsPerishable {
		flags |= rulePerishable
	}
	var rules []*filterRule
	for _, pat := range strings.Fields(defaultCVSIgnore) {
		rules = append(rules, newRule(pat, flags))
	}
	return rules
}

func newRule(pat string, flags int) *filterRule {
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
	return rule
}

func (l *filterList) add(pat string, flags int) {
	l.rules = append(l.rules, newRule(pat, flags))
}

// scopeTree builds the filter scope of each directory below a traversal
// root the first time it is needed.
type scopeTree struct {
	l      *filterList
	root   string
	dir    string
	read   func(string) ([]byte, error)
	scopes map[string]*filterScope
}

// newScopeTree starts at dir, the storage path of the first directory
// traversed. root is the storage path of the transfer root.
func (l *filterList) newScopeTree(root, dir string, read func(string) ([]byte, error)) *scopeTree {
	return &scopeTree{l: l, root: root, dir: dir, read: read, scopes: map[string]*filterScope{}}
}

// scope returns the scope for rel, a directory relative to the tree's
// first directory, which is ".".
func (t *scopeTree) scope(rel string) (*filterScope, error) {
	if sc, ok := t.scopes[rel]; ok {
		return sc, nil
	}
	if err := t.l.reserveScope(len(rel) + int(unsafe.Sizeof(rel)) + int(unsafe.Sizeof((*filterScope)(nil)))); err != nil {
		return nil, err
	}
	var sc *filterScope
	if rel == "." {
		var err error
		if sc, err = t.l.newScope(t.root, t.dir, t.read); err != nil {
			return nil, err
		}
	} else {
		parent, err := t.scope(path.Dir(rel))
		if err != nil {
			return nil, err
		}
		if sc, err = t.l.enter(parent, path.Join(t.dir, rel), t.read); err != nil {
			return nil, err
		}
	}
	t.scopes[rel] = sc
	return sc, nil
}

// mergeReader reads per-directory merge files from storage, returning nil
// for a missing one. files lists the regular files below tree, relative to
// it; other files are looked up in their directory, as not every backend
// reports a missing file from Open.
func (s *session) mergeReader(tree string, files map[string]bool) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		rel, under := name, tree == ""
		if !under {
			rel, under = strings.CutPrefix(name, tree+"/")
		}
		found := false
		if under && files != nil {
			found = files[rel]
		} else {
			dir := path.Dir(name)
			if dir == "." {
				dir = ""
			}
			entries, err := s.fs.ReadDir(dir, false)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			base := path.Base(name)
			found = slices.ContainsFunc(entries, func(e FileInfo) bool {
				return !e.IsDir && strings.Trim(e.Name, "/") == base
			})
		}
		if !found {
			return nil, nil
		}
		f, info, err := s.fs.Open(name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		if info.Size > maxMergeFileSize {
			return nil, fmt.Errorf("larger than %d bytes", maxMergeFileSize)
		}
		buf := make([]byte, info.Size)
		if err := readFullAt(f, buf, 0); err != nil {
			return nil, err
		}
		return buf, nil
	}
}

// newScope returns the scope for dir, the storage path of the first
// directory traversed. A merge rule whose name has a directory part that
// is an ancestor of dir first reads the file in that directory and each
// one below it down to dir's parent (setup_merge_file).
func (l *filterList) newScope(root, dir string, read func(string) ([]byte, error)) (*filterScope, error) {
	if err := l.reserveScope(int(unsafe.Sizeof(filterScope{})) + len(root) + len(dir) + int(unsafe.Sizeof([]*filterRule{}))*len(l.merges)); err != nil {
		return nil, err
	}
	parent := &filterScope{root: root, lists: make([][]*filterRule, len(l.merges))}
	for i, m := range l.merges {
		if !strings.Contains(m.pattern, "/") {
			continue
		}
		from := path.Dir(m.pattern)
		if !strings.HasPrefix(from, "/") {
			from = path.Join("/"+dir, from)
		}
		from = strings.TrimPrefix(path.Clean(from), "/")
		if from == dir || (from != "" && !strings.HasPrefix(dir, from+"/")) {
			continue
		}
		rest := strings.Split(strings.TrimPrefix(dir, from), "/")
		d := from
		for _, elem := range rest {
			if elem == "" {
				continue
			}
			rules, cleared, err := l.readMerge(m, d, read)
			if err != nil {
				return nil, err
			}
			if cleared {
				parent.lists[i] = nil
			}
			if m.flags&ruleNoInherit == 0 {
				if parent.lists[i], err = l.inheritRules(rules, parent.lists[i]); err != nil {
					return nil, err
				}
			}
			d = path.Join(d, elem)
		}
	}
	return l.enter(parent, dir, read)
}

// enter returns the scope for dir, a storage path, below parent's
// directory (push_local_filters).
func (l *filterList) enter(parent *filterScope, dir string, read func(string) ([]byte, error)) (*filterScope, error) {
	if len(l.merges) == 0 {
		return parent, nil
	}
	if err := l.reserveScope(int(unsafe.Sizeof(filterScope{})) + len(dir) + int(unsafe.Sizeof([]*filterRule{}))*len(l.merges)); err != nil {
		return nil, err
	}
	sc := &filterScope{root: parent.root, lists: make([][]*filterRule, len(l.merges))}
	for i, m := range l.merges {
		var inherited []*filterRule
		if m.flags&ruleNoInherit == 0 {
			inherited = parent.lists[i]
		}
		rules, cleared, err := l.readMerge(m, dir, read)
		if err != nil {
			return nil, err
		}
		if cleared {
			inherited = nil
		}
		if sc.lists[i], err = l.inheritRules(rules, inherited); err != nil {
			return nil, err
		}
	}
	return sc, nil
}

func (l *filterList) reserveScope(size int) error {
	if size > maxFileListBytes-l.scopeBytes {
		return errFilterListLimit
	}
	l.scopeBytes += size
	return nil
}

func (l *filterList) inheritRules(rules, inherited []*filterRule) ([]*filterRule, error) {
	if len(rules) == 0 {
		return inherited, nil
	}
	if len(inherited) == 0 {
		return rules, nil
	}
	if err := l.reserveScope(int(unsafe.Sizeof((*filterRule)(nil))) * (len(rules) + len(inherited))); err != nil {
		return nil, err
	}
	return append(rules, inherited...), nil
}

// readMerge parses the merge file for m in dir (parse_filter_file). A "!"
// clears the rules before it, inherited ones included.
func (l *filterList) readMerge(m *filterRule, dir string, read func(string) ([]byte, error)) (rules []*filterRule, cleared bool, err error) {
	name := path.Join(dir, path.Base(m.pattern))
	data, err := read(name)
	if err != nil {
		return nil, false, &mergeFileError{name: name, err: err}
	}
	if len(data) > maxFilterBytes-l.mergeBytes {
		return nil, false, &mergeFileError{name: name, err: errFilterListLimit}
	}
	l.mergeBytes += len(data)
	var toks []string
	if m.flags&ruleWordSplit != 0 {
		toks = strings.FieldsFunc(string(data), func(r rune) bool { return r < 0x80 && isSpace(byte(r)) })
	} else {
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		for line := range strings.FieldsFuncSeq(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
			if line[0] != '#' && line[0] != ';' {
				toks = append(toks, line)
			}
		}
	}
	for _, tok := range toks {
		rule, err := parseRuleTok(tok, m.flags, false)
		if err != nil {
			return nil, false, &mergeFileError{name: name, err: err}
		}
		if rule == nil {
			continue
		}
		switch {
		case rule.flags&ruleClearList != 0:
			rules, cleared = nil, true
			continue
		case rule.flags&ruleMergeFile != 0:
			return nil, false, &mergeFileError{name: name, err: errors.New("merge rules inside a merge file are not supported")}
		case rule.flags&ruleCVSIgnore != 0:
			defaults := l.cvsIgnoreRules()
			if len(defaults) > maxFilterRules-l.mergeRules-len(l.rules)-len(rules) {
				return nil, false, &mergeFileError{name: name, err: errFilterListLimit}
			}
			rules = append(rules, defaults...)
			continue
		}
		if l.deleteExcluded && rule.flags&sideFlags == 0 {
			rule.flags |= ruleSenderSide
		}
		if rule.flags&sideFlags == l.otherSide {
			continue
		}
		pat := rule.pattern
		if rule.flags&ruleAbsPath == 0 && strings.HasPrefix(pat, "/") {
			// Anchored patterns are relative to the file's directory.
			rule.flags |= ruleAbsPath
			if dir != "" {
				pat = "/" + dir + pat
			}
		}
		r := newRule(pat, rule.flags)
		if len(rules) >= maxFilterRules-l.mergeRules-len(l.rules) {
			return nil, false, &mergeFileError{name: name, err: errFilterListLimit}
		}
		rules = append(rules, r)
	}
	l.mergeRules += len(rules)
	return rules, cleared, nil
}

// check returns -1 if name is excluded, 1 if it is included and 0 when no
// rule matched. name is relative to the transfer root. sc holds the
// per-directory rules for name's directory, if any have been read.
func (l *filterList) check(name string, isDir bool, sc *filterScope) int {
	return l.checkPerishable(name, isDir, sc, false)
}

// checkPerishable is check, skipping perishable rules when
// ignorePerishable is set (rsync's ignore_perishable).
func (l *filterList) checkPerishable(name string, isDir bool, sc *filterScope, ignorePerishable bool) int {
	root := ""
	if sc != nil {
		root = sc.root
	}
	for _, r := range l.rules {
		if ignorePerishable && r.flags&rulePerishable != 0 {
			continue
		}
		if r.flags&rulePerdirMerge != 0 {
			if sc != nil {
				if rc := checkRules(sc.lists[r.merge], name, isDir, root, ignorePerishable); rc != 0 {
					return rc
				}
			}
			continue
		}
		if rc := checkRules([]*filterRule{r}, name, isDir, root, ignorePerishable); rc != 0 {
			return rc
		}
	}
	return 0
}

func checkRules(rules []*filterRule, name string, isDir bool, root string, ignorePerishable bool) int {
	for _, r := range rules {
		if ignorePerishable && r.flags&rulePerishable != 0 {
			continue
		}
		if r.matches(name, isDir, root) {
			if r.flags&ruleInclude != 0 {
				return 1
			}
			return -1
		}
	}
	return 0
}

func (l *filterList) excluded(name string, isDir bool, sc *filterScope) bool {
	return l != nil && l.check(name, isDir, sc) < 0
}

// protectsInDeletedDir reports whether a rule keeps name, which is inside a
// directory being deleted. Perishable rules don't, so the directory can go.
func (l *filterList) protectsInDeletedDir(name string, isDir bool, sc *filterScope) bool {
	return l != nil && l.checkPerishable(name, isDir, sc, true) < 0
}

// matches is rsync's rule_matches. Absolute patterns match the storage
// path, which is name below root.
func (r *filterRule) matches(fname string, isDir bool, root string) bool {
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
	case r.flags&ruleAbsPath != 0 && !strings.HasPrefix(fname, "/") && root != "":
		strs = append(strs, root, "/")
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
