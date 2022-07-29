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

var reTimestamp = regexp.MustCompile(`T.+`)

func ParseText(text string) (*ParsedText, error) {
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
		return &ParsedText{}, err
	}
	metaData := meta.Get(context)

	var publishAt *time.Time = nil
	var err error
	date := toString(metaData["date"])
	if date != "" {
		if strings.Contains(date, "T") {
			date = reTimestamp.ReplaceAllString(date, "")
		}

		nextDate, err := time.Parse("2006-01-02", date)
		if err != nil {
			return &ParsedText{}, err
		}
		publishAt = &nextDate
	}

	nav, err := toLinks(metaData["nav"])
	if err != nil {
		return &ParsedText{}, err
	}

	return &ParsedText{
		Html: buf.String(),
		MetaData: &MetaData{
			PublishAt:   publishAt,
			Title:       toString(metaData["title"]),
			Description: toString(metaData["description"]),
			Nav:         nav,
		},
	}, nil
}
