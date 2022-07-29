package pkg

import (
	"fmt"
	"html/template"
	"strings"
	"time"
)

type ParsedText struct {
	Items    []*ListItem
	MetaData *MetaData
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
}

type MetaData struct {
	PublishAt   *time.Time
	Title       string
	Description string
	ListType    string // https://developer.mozilla.org/en-US/docs/Web/CSS/list-style-type
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
	t, err := time.Parse("2006-01-02", date)
	return &t, err
}

func TokenToMetaField(meta *MetaData, token *SplitToken) {
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
	}
}

func KeyAsValue(token *SplitToken) string {
	if token.Value == "" {
		return token.Key
	}
	return token.Value
}

func ParseText(text string) *ParsedText {
	textItems := SplitByNewline(text)
	items := []*ListItem{}
	meta := &MetaData{
		ListType: "disc",
	}
	pre := false
	skip := false
	var prevItem *ListItem

	for _, t := range textItems {
		skip = false

		if len(items) > 0 {
			prevItem = items[len(items)-1]
		}

		li := &ListItem{
			Value: strings.Trim(t, " "),
		}

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
			li.URL = template.URL(split.Key)
			li.Value = KeyAsValue(split)
		} else if strings.HasPrefix(li.Value, varToken) {
			split := TextToSplitToken(strings.Replace(li.Value, varToken, "", 1))
			TokenToMetaField(meta, split)
			continue
		} else if strings.HasPrefix(li.Value, headerTwoToken) {
			li.IsHeaderTwo = true
			li.Value = strings.Replace(li.Value, headerTwoToken, "", 1)
		} else if strings.HasPrefix(li.Value, headerOneToken) {
			li.IsHeaderOne = true
			li.Value = strings.Replace(li.Value, headerOneToken, "", 1)
		} else {
			li.IsText = true
		}

		if li.IsText && li.Value == "" {
			skip = true
		}

		if !skip {
			items = append(items, li)
		}
	}

	return &ParsedText{
		Items:    items,
		MetaData: meta,
	}
}
