package prose

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alecthomas/chroma/formatters/html"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

type MetaData struct {
	PublishAt   *time.Time
	Title       string
	Description string
	Nav         []Link
	Tags        []string
}

type ParsedText struct {
	Html string
	*MetaData
}

func toString(obj interface{}) string {
	if obj == nil {
		return ""
	}
	return obj.(string)
}

func toLinks(obj interface{}) ([]Link, error) {
	links := []Link{}
	if obj == nil {
		return links, nil
	}

	addLinks := func(raw map[interface{}]interface{}) {
		for k, v := range raw {
			links = append(links, Link{
				Text: k.(string),
				URL:  v.(string),
			})
		}
	}

	switch raw := obj.(type) {
	case map[interface{}]interface{}:
		addLinks(raw)
	case []interface{}:
		for _, v := range raw {
			switch linkRaw := v.(type) {
			case map[interface{}]interface{}:
				addLinks(v.(map[interface{}]interface{}))
			default:
				return links, fmt.Errorf("unsupported type for `nav` link item (%T), looking for map (`text: href`)", linkRaw)
			}
		}
	default:
		return links, fmt.Errorf("unsupported type for `nav` variable: %T", raw)
	}

	return links, nil
}

func toTags(obj interface{}) ([]string, error) {
	arr := make([]string, 0)
	if obj == nil {
		return arr, nil
	}

	switch raw := obj.(type) {
	case []interface{}:
		for _, tag := range raw {
			arr = append(arr, tag.(string))
		}
	case string:
		tags := strings.Split(raw, " ")
		for _, tag := range tags {
			arr = append(arr, strings.TrimSpace(tag))
		}
	default:
		return arr, fmt.Errorf("unsupported type for `tags` variable: %T", raw)
	}

	return arr, nil
}

var reTimestamp = regexp.MustCompile(`T.+`)

func ParseText(text string) (*ParsedText, error) {
	parsed := ParsedText{
		MetaData: &MetaData{
			Tags: []string{},
		},
	}
	var buf bytes.Buffer
	hili := highlighting.NewHighlighting(
		highlighting.WithFormatOptions(
			html.WithLineNumbers(true),
			html.WithClasses(true),
		),
	)
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			meta.Meta,
			hili,
		),
	)
	context := parser.NewContext()
	if err := md.Convert([]byte(text), &buf, parser.WithContext(context)); err != nil {
		return &parsed, err
	}
	parsed.Html = buf.String()
	metaData := meta.Get(context)
	parsed.MetaData.Title = toString(metaData["title"])
	parsed.MetaData.Description = toString(metaData["description"])

	var publishAt *time.Time = nil
	var err error
	date := toString(metaData["date"])
	if date != "" {
		if strings.Contains(date, "T") {
			date = reTimestamp.ReplaceAllString(date, "")
		}

		nextDate, err := time.Parse("2006-01-02", date)
		if err != nil {
			return &parsed, err
		}
		publishAt = &nextDate
	}
	parsed.MetaData.PublishAt = publishAt

	nav, err := toLinks(metaData["nav"])
	if err != nil {
		return &parsed, err
	}
	parsed.MetaData.Nav = nav

	tags, err := toTags(metaData["tags"])
	if err != nil {
		return &parsed, err
	}
	parsed.MetaData.Tags = tags

	return &parsed, nil
}
