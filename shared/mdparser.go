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
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
	gtext "github.com/yuin/goldmark/text"
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

func toString(obj interface{}) (string, error) {
	if obj == nil {
		return "", nil
	}
	switch val := obj.(type) {
	case string:
		return val, nil
	default:
		return "", fmt.Errorf("incorrect type for value: %T, should be string", val)
	}
}

func toBool(obj interface{}) (bool, error) {
	if obj == nil {
		return false, nil
	}
	switch val := obj.(type) {
	case bool:
		return val, nil
	default:
		return false, fmt.Errorf("incorrect type for value: %T, should be bool", val)
	}
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

	// we do the Parse/Render steps manually to get a chance to examine the AST
	btext := []byte(text)
	doc := md.Parser().Parse(gtext.NewReader(btext), parser.WithContext(context))
	if err := md.Renderer().Render(&buf, btext, doc); err != nil {
		return &parsed, err
	}

	parsed.Html = policy.Sanitize(buf.String())
	metaData := meta.Get(context)

	// title:
	// 1. if specified in frontmatter, use that
	title, err := toString(metaData["title"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "title", err)
	}
	parsed.MetaData.Title = title
	// 2. If an <h1> is found before a <p> or other heading is found, use that
	if parsed.MetaData.Title == "" {
		ast.Walk(doc, func (n ast.Node, entering bool) (ast.WalkStatus, error) {
			if n.Kind() == ast.KindHeading {
				if h := n.(*ast.Heading); h.Level == 1 {
					parsed.MetaData.Title = string(h.Text(btext))
				}
				return ast.WalkStop, nil
			}
			if ast.IsParagraph(n) {
				return ast.WalkStop, nil
			}
			return ast.WalkContinue, nil
		})
	}
	// 3. else, set it to nothing (slug should get used later down the line)
	// this is implicit since it's already ""

	description, err := toString(metaData["description"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "description", err)
	}
	parsed.MetaData.Description = description

	layout, err := toString(metaData["layout"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "layout", err)
	}
	parsed.MetaData.Layout = layout

	image, err := toString(metaData["image"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "image", err)
	}
	parsed.MetaData.Image = image

	card, err := toString(metaData["card"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "card", err)
	}
	parsed.MetaData.ImageCard = card

	hidden, err := toBool(metaData["draft"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "draft", err)
	}
	parsed.MetaData.Hidden = hidden

	favicon, err := toString(metaData["favicon"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "favicon", err)
	}
	parsed.MetaData.Favicon = favicon

	var publishAt *time.Time = nil
	date, err := toString(metaData["date"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "date", err)
	}

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
