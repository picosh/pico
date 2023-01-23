package shared

import (
	"fmt"
	"html/template"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"golang.org/x/exp/slices"
)

var reIndent = regexp.MustCompile(`^[[:blank:]]+`)
var DigestIntervalOpts = []string{
	"10min",
	"1hour",
	"6hour",
	"12hour",
	"1day",
	"7day",
	"30day",
}

type ListParsedText struct {
	Items []*ListItem
	*ListMetaData
}

type ListItem struct {
	Value       string
	URL         template.URL
	Variable    string
	IsURL       bool
	IsBlock     bool
	IsText      bool
	IsHeaderOne bool
	IsHeaderTwo bool
	IsImg       bool
	IsPre       bool
	Indent      int
}

type ListMetaData struct {
	PublishAt      *time.Time
	Title          string
	Description    string
	Layout         string
	Tags           []string
	ListType       string // https://developer.mozilla.org/en-US/docs/Web/CSS/list-style-type
	DigestInterval string
	Email          string
	InlineContent  bool // allows content inlining to be disabled in feeds.sh emails
}

var urlToken = "=>"
var blockToken = ">"
var varToken = "=:"
var imgToken = "=<"
var headerOneToken = "#"
var headerTwoToken = "##"
var preToken = "```"

type SplitToken struct {
	Key   string
	Value string
}

func TextToSplitToken(text string) *SplitToken {
	txt := strings.Trim(text, " ")
	token := &SplitToken{}
	word := ""
	for i, c := range txt {
		if c == ' ' {
			token.Key = strings.Trim(word, " ")
			token.Value = strings.Trim(txt[i:], " ")
			break
		} else {
			word += string(c)
		}
	}

	if token.Key == "" {
		token.Key = strings.Trim(text, " ")
		token.Value = strings.Trim(text, " ")
	}

	return token
}

func SplitByNewline(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func PublishAtDate(date string) (*time.Time, error) {
	t, err := dateparse.ParseStrict(date)
	return &t, err
}

func TokenToMetaField(meta *ListMetaData, token *SplitToken) error {
	if token.Key == "publish_at" {
		publishAt, err := PublishAtDate(token.Value)
		if err == nil {
			meta.PublishAt = publishAt
		}
	} else if token.Key == "title" {
		meta.Title = token.Value
	} else if token.Key == "description" {
		meta.Description = token.Value
	} else if token.Key == "list_type" {
		meta.ListType = token.Value
	} else if token.Key == "tags" {
		tags := strings.Split(token.Value, ",")
		meta.Tags = make([]string, 0)
		for _, tag := range tags {
			meta.Tags = append(meta.Tags, strings.TrimSpace(tag))
		}
	} else if token.Key == "layout" {
		meta.Layout = token.Value
	} else if token.Key == "digest_interval" {
		if !slices.Contains(DigestIntervalOpts, token.Value) {
			return fmt.Errorf(
				"(%s) is not a valid option, choose from [%s]",
				token.Value,
				strings.Join(DigestIntervalOpts, ","),
			)
		}
		meta.DigestInterval = token.Value
	} else if token.Key == "email" {
		meta.Email = token.Value
	} else if token.Key == "inline_content" {
		v, err := strconv.ParseBool(token.Value)
		if err != nil {
			// its empty or its improperly configured, just send the content
			v = true
		}
		meta.InlineContent = v
	}

	return nil
}

func KeyAsValue(token *SplitToken) string {
	if token.Value == "" {
		return token.Key
	}
	return token.Value
}

func parseItem(meta *ListMetaData, li *ListItem, prevItem *ListItem, pre bool, mod int, linkify Linkify) (bool, bool, int) {
	skip := false

	if strings.HasPrefix(li.Value, preToken) {
		pre = !pre
		if pre {
			nextValue := strings.Replace(li.Value, preToken, "", 1)
			li.IsPre = true
			li.Value = nextValue
		} else {
			skip = true
		}
	} else if pre {
		nextValue := strings.Replace(li.Value, preToken, "", 1)
		prevItem.Value = fmt.Sprintf("%s\n%s", prevItem.Value, nextValue)
		skip = true
	} else if strings.HasPrefix(li.Value, urlToken) {
		li.IsURL = true
		split := TextToSplitToken(strings.Replace(li.Value, urlToken, "", 1))
		li.URL = template.URL(split.Key)
		li.Value = KeyAsValue(split)
	} else if strings.HasPrefix(li.Value, blockToken) {
		li.IsBlock = true
		li.Value = strings.Replace(li.Value, blockToken, "", 1)
	} else if strings.HasPrefix(li.Value, imgToken) {
		li.IsImg = true
		split := TextToSplitToken(strings.Replace(li.Value, imgToken, "", 1))
		key := split.Key
		if strings.HasPrefix(key, "/") {
			frag := SanitizeFileExt(key)
			key = linkify.Create(frag)
		} else if strings.HasPrefix(key, "./") {
			name := SanitizeFileExt(key[1:])
			key = linkify.Create(name)
		}
		li.URL = template.URL(key)
		li.Value = KeyAsValue(split)
	} else if strings.HasPrefix(li.Value, varToken) {
		split := TextToSplitToken(strings.Replace(li.Value, varToken, "", 1))
		err := TokenToMetaField(meta, split)
		if err != nil {
			log.Println(err)
		}
	} else if strings.HasPrefix(li.Value, headerTwoToken) {
		li.IsHeaderTwo = true
		li.Value = strings.Replace(li.Value, headerTwoToken, "", 1)
	} else if strings.HasPrefix(li.Value, headerOneToken) {
		li.IsHeaderOne = true
		li.Value = strings.Replace(li.Value, headerOneToken, "", 1)
	} else if reIndent.MatchString(li.Value) {
		trim := reIndent.ReplaceAllString(li.Value, "")
		old := len(li.Value)
		li.Value = trim

		pre, skip, _ = parseItem(meta, li, prevItem, pre, mod, linkify)
		if prevItem != nil && prevItem.Indent == 0 {
			mod = old - len(trim)
			li.Indent = 1
		} else {
			numerator := old - len(trim)
			if mod == 0 {
				li.Indent = 1
			} else {
				li.Indent = numerator / mod
			}
		}
	} else {
		li.IsText = true
	}

	return pre, skip, mod
}

func ListParseText(text string, linkify Linkify) *ListParsedText {
	textItems := SplitByNewline(text)
	items := []*ListItem{}
	meta := ListMetaData{
		ListType: "disc",
		Tags:     []string{},
		Layout:   "default",
	}
	pre := false
	skip := false
	mod := 0
	var prevItem *ListItem

	for _, t := range textItems {
		if len(items) > 0 {
			prevItem = items[len(items)-1]
		}

		li := ListItem{
			Value: t,
		}

		pre, skip, mod = parseItem(&meta, &li, prevItem, pre, mod, linkify)

		if li.IsText && li.Value == "" {
			skip = true
		}

		if !skip {
			items = append(items, &li)
		}
	}

	return &ListParsedText{
		Items:        items,
		ListMetaData: &meta,
	}
}
