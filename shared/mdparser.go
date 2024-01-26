package shared

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/alecthomas/chroma/formatters/html"
	"github.com/araddon/dateparse"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"go.abhg.dev/goldmark/anchor"
	yaml "gopkg.in/yaml.v2"
)

type Link struct {
	URL  string
	Text string
}

type MetaData struct {
	PublishAt   *time.Time
	Title       string
	Description string
	Nav         []Link
	Tags        []string
	Aliases     []string
	Layout      string
	Image       string
	ImageCard   string
	Favicon     string
	Hidden      bool
}

type ParsedText struct {
	Html string
	*MetaData
}

func HtmlPolicy() *bluemonday.Policy {
	policy := bluemonday.UGCPolicy()
	policy.AllowStyling()
	policy.AllowAttrs("rel").OnElements("a")
	return policy
}

var policy = HtmlPolicy()

func toString(obj interface{}) string {
	if obj == nil {
		return ""
	}
	return obj.(string)
}

func toBool(obj interface{}) bool {
	if obj == nil {
		return false
	}
	return obj.(bool)
}

func toLinks(orderedMetaData yaml.MapSlice) ([]Link, error) {
	var navData interface{}
	for i := 0; i < len(orderedMetaData); i++ {
		var item = orderedMetaData[i]
		if item.Key == "nav" {
			navData = item.Value
			break
		}
	}

	links := []Link{}
	if navData == nil {
		return links, nil
	}

	addLinks := func(raw yaml.MapSlice) {
		for _, k := range raw {
			links = append(links, Link{
				Text: k.Key.(string),
				URL:  k.Value.(string),
			})
		}
	}

	switch raw := navData.(type) {
	case yaml.MapSlice:
		addLinks(raw)
	case []interface{}:
		for _, v := range raw {
			switch linkRaw := v.(type) {
			case yaml.MapSlice:
				addLinks(v.(yaml.MapSlice))
			default:
				return links, fmt.Errorf("unsupported type for `nav` link item (%T), looking for map (`text: href`)", linkRaw)
			}
		}
	default:
		return links, fmt.Errorf("unsupported type for `nav` variable: %T", raw)
	}

	return links, nil
}

func toAliases(obj interface{}) ([]string, error) {
	arr := make([]string, 0)
	if obj == nil {
		return arr, nil
	}

	switch raw := obj.(type) {
	case []interface{}:
		for _, alias := range raw {
			als := strings.TrimSpace(alias.(string))
			arr = append(arr, strings.TrimPrefix(als, "/"))
		}
	case string:
		aliases := strings.Split(raw, " ")
		for _, alias := range aliases {
			als := strings.TrimSpace(alias)
			arr = append(arr, strings.TrimPrefix(als, "/"))
		}
	default:
		return arr, fmt.Errorf("unsupported type for `aliases` variable: %T", raw)
	}

	return arr, nil
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

func ParseText(text string) (*ParsedText, error) {
	parsed := ParsedText{
		MetaData: &MetaData{
			Tags:    []string{},
			Aliases: []string{},
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
			extension.Footnote,
			meta.Meta,
			hili,
			&anchor.Extender{
				Position: anchor.Before,
				Texter:   anchor.Text("#"),
			},
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			ghtml.WithUnsafe(),
		),
	)
	context := parser.NewContext()
	if err := md.Convert([]byte(text), &buf, parser.WithContext(context)); err != nil {
		return &parsed, err
	}

	parsed.Html = policy.Sanitize(buf.String())
	metaData := meta.Get(context)
	parsed.MetaData.Title = toString(metaData["title"])
	parsed.MetaData.Description = toString(metaData["description"])
	parsed.MetaData.Layout = toString(metaData["layout"])
	parsed.MetaData.Image = toString(metaData["image"])
	parsed.MetaData.ImageCard = toString(metaData["card"])
	parsed.MetaData.Hidden = toBool(metaData["draft"])
	parsed.MetaData.Favicon = toString(metaData["favicon"])

	var publishAt *time.Time = nil
	var err error
	date := toString(metaData["date"])
	if date != "" {
		nextDate, err := dateparse.ParseStrict(date)
		if err != nil {
			return &parsed, err
		}
		publishAt = &nextDate
	}
	parsed.MetaData.PublishAt = publishAt

	orderedMetaData := meta.GetItems(context)

	nav, err := toLinks(orderedMetaData)
	if err != nil {
		return &parsed, err
	}
	parsed.MetaData.Nav = nav

	aliases, err := toAliases(metaData["aliases"])
	if err != nil {
		return &parsed, err
	}
	parsed.MetaData.Aliases = aliases

	tags, err := toTags(metaData["tags"])
	if err != nil {
		return &parsed, err
	}
	parsed.MetaData.Tags = tags

	return &parsed, nil
}
