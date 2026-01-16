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
)

/*
=> https://www.youtube.com/watch?v=HxaD_trXwRE Lexical Scanning in Go - Rob Pike
func run() {
	for state := startState; state != nil {
		state = stateFn(lexer);
	}
}
*/

var reIndent = regexp.MustCompile(`^[[:blank:]]+`)

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
	IsHr        bool
	Indent      int
}

type ListMetaData struct {
	// prose
	Aliases     []string
	Description string
	Hidden      bool
	Image       string
	ImageCard   string
	Layout      string
	PublishAt   *time.Time
	Tags        []string
	Title       string

	// feeds
	DigestInterval string
	Cron           string
	Email          string
	InlineContent  bool // allows content inlining to be disabled in feeds.pico.sh emails
}

var urlToken = "=>"
var blockToken = ">"
var varToken = "=:"
var imgToken = "=<"
var headerOneToken = "#"
var headerTwoToken = "##"
var preToken = "```"
var hrToken = "=="

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
	switch token.Key {
	case "date":
		publishAt, err := PublishAtDate(token.Value)
		if err == nil {
			meta.PublishAt = publishAt
		}
	case "title":
		meta.Title = token.Value
	case "description":
		meta.Description = token.Value
	case "image":
		meta.Image = token.Value
	case "image_card":
		meta.ImageCard = token.Value
	case "draft":
		if token.Value == "true" {
			meta.Hidden = true
		} else {
			meta.Hidden = false
		}
	case "tags":
		tags := strings.Split(token.Value, ",")
		meta.Tags = make([]string, 0)
		for _, tag := range tags {
			meta.Tags = append(meta.Tags, strings.TrimSpace(tag))
		}
	case "aliases":
		aliases := strings.Split(token.Value, ",")
		meta.Aliases = make([]string, 0)
		for _, alias := range aliases {
			meta.Aliases = append(meta.Aliases, strings.TrimSpace(alias))
		}
	case "layout":
		meta.Layout = token.Value
	case "digest_interval":
		meta.DigestInterval = token.Value
	case "cron":
		meta.Cron = token.Value
	case "email":
		meta.Email = token.Value
	case "inline_content":
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

func parseItem(meta *ListMetaData, li *ListItem, prevItem *ListItem, pre bool, mod int) (bool, bool, int) {
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
	} else if strings.HasPrefix(li.Value, hrToken) {
		li.IsHr = true
		li.Value = ""
	} else if reIndent.MatchString(li.Value) {
		trim := reIndent.ReplaceAllString(li.Value, "")
		old := len(li.Value)
		li.Value = trim

		pre, skip, _ = parseItem(meta, li, prevItem, pre, mod)
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

func ListParseText(text string) *ListParsedText {
	textItems := SplitByNewline(text)
	items := []*ListItem{}
	meta := ListMetaData{
		Aliases:       []string{},
		InlineContent: true,
		Layout:        "default",
		PublishAt:     &time.Time{},
		Tags:          []string{},
		Hidden:        false,
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

		pre, skip, mod = parseItem(&meta, &li, prevItem, pre, mod)

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
