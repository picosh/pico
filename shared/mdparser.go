package shared

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/araddon/dateparse"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
	gtext "github.com/yuin/goldmark/text"
	"go.abhg.dev/goldmark/anchor"
	"go.abhg.dev/goldmark/hashtag"
	"go.abhg.dev/goldmark/toc"
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
	WithStyles  bool
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

func toBool(obj interface{}, fallback bool) (bool, error) {
	if obj == nil {
		return fallback, nil
	}
	switch val := obj.(type) {
	case bool:
		return val, nil
	default:
		return false, fmt.Errorf("incorrect type for value: %T, should be bool", val)
	}
}

// The toc frontmatter can take a boolean or an integer.
//
// A value of -1 or false means "do not generate a toc".
// A value of 0 or true means "generate a toc with no depth limit".
// A value of >0 means "generate a toc with a depth limit of $value past title".
func toToc(obj interface{}) (int, error) {
	if obj == nil {
		return -1, nil
	}
	switch val := obj.(type) {
	case bool:
		if val {
			return 0, nil
		}
		return -1, nil
	case int:
		if val < -1 {
			val = -1
		}
		return val, nil
	default:
		return -1, fmt.Errorf("incorrect type for value: %T, should be bool or int", val)
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

func CreateGoldmark(extenders ...goldmark.Extender) goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extenders...,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			ghtml.WithUnsafe(),
		),
	)
}

func ParseText(text string) (*ParsedText, error) {
	parsed := ParsedText{
		MetaData: &MetaData{
			Tags:       []string{},
			Aliases:    []string{},
			WithStyles: true,
		},
	}
	hili := highlighting.NewHighlighting(
		highlighting.WithFormatOptions(
			html.WithLineNumbers(true),
			html.WithClasses(true),
		),
	)
	extenders := []goldmark.Extender{
		extension.GFM,
		extension.Footnote,
		meta.Meta,
		&hashtag.Extender{},
		hili,
		&anchor.Extender{
			Position: anchor.After,
			Texter:   anchor.Text("#"),
		},
	}
	md := CreateGoldmark(extenders...)
	context := parser.NewContext()
	// we do the Parse/Render steps manually to get a chance to examine the AST
	btext := []byte(text)
	doc := md.Parser().Parse(gtext.NewReader(btext), parser.WithContext(context))
	metaData := meta.Get(context)

	// title:
	// 1. if specified in frontmatter, use that
	title, err := toString(metaData["title"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "title", err)
	}
	// 2. If an <h1> is found before a <p> or other heading is found, use that
	if title == "" {
		title = AstTitle(doc, btext, true)
	}
	// 3. else, set it to nothing (slug should get used later down the line)
	// this is implicit since it's already ""
	parsed.MetaData.Title = title

	// only handle toc after the title is extracted (if it's getting extracted)
	mtoc, err := toToc(metaData["toc"])
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "toc", err)
	}
	if mtoc >= 0 {
		err = AstToc(doc, btext, mtoc)
		if err != nil {
			return &parsed, fmt.Errorf("error generating toc: %w", err)
		}
	}

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

	hidden, err := toBool(metaData["draft"], false)
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "draft", err)
	}
	parsed.MetaData.Hidden = hidden

	withStyles, err := toBool(metaData["with_styles"], true)
	if err != nil {
		return &parsed, fmt.Errorf("front-matter field (%s): %w", "with_style", err)
	}
	parsed.MetaData.WithStyles = withStyles

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

	rtags := metaData["tags"]
	tags, err := toTags(rtags)
	if err != nil {
		return &parsed, err
	}
	// fill from hashtag ASTs as fallback
	if rtags == nil {
		tags = AstTags(doc)
	}
	parsed.MetaData.Tags = tags

	// Rendering happens last to allow any of the previous steps to manipulate
	// the AST.
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, btext, doc); err != nil {
		return &parsed, err
	}
	parsed.Html = policy.Sanitize(buf.String())

	return &parsed, nil
}

func AstTags(doc ast.Node) []string {
	var tags []string
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n.Kind() {
		// ignore hashtags inside of these sections
		case ast.KindBlockquote, ast.KindCodeBlock, ast.KindCodeSpan:
			return ast.WalkSkipChildren, nil
		// register hashtags
		case hashtag.Kind:
			t := n.(*hashtag.Node)
			if entering { // only add each tag once
				tags = append(tags, string(t.Tag))
			}
		}
		// out-of-switch default
		return ast.WalkContinue, nil
	})
	if err != nil {
		panic(err) // unreachable
	}

	// sort and deduplicate results
	dedupe := removeDuplicateStr(tags)
	return dedupe
}

// https://stackoverflow.com/a/66751055
func removeDuplicateStr(strSlice []string) []string {
	allKeys := make(map[string]bool)
	list := []string{}
	for _, item := range strSlice {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}
	return list
}

// AstTitle extracts the title (if any) from a parsed markdown document.
//
// If "clean" is true, it will also remove the heading node from the AST.
func AstTitle(doc ast.Node, src []byte, clean bool) string {
	out := ""
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if n.Kind() == ast.KindHeading {
			if h := n.(*ast.Heading); h.Level == 1 {
				if clean {
					p := h.Parent()
					p.RemoveChild(p, n)
				}
				out = string(h.Lines().Value(src))
			}
			return ast.WalkStop, nil
		}
		if ast.IsParagraph(n) {
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		panic(err) // unreachable
	}
	return out
}

func AstToc(doc ast.Node, src []byte, mtoc int) error {
	var tree *toc.TOC
	if mtoc >= 0 {
		var err error
		if mtoc > 0 {
			tree, err = toc.Inspect(doc, src, toc.Compact(true), toc.MinDepth(2), toc.MaxDepth(mtoc+1))
		} else {
			tree, err = toc.Inspect(doc, src, toc.Compact(true), toc.MinDepth(2))
		}
		if err != nil {
			return err
		}
		if tree == nil {
			return nil // no headings?
		}
	}
	list := toc.RenderList(tree)
	if list == nil {
		return nil // no headings
	}

	list.SetAttributeString("id", []byte("toc-list"))

	// generate # toc
	heading := ast.NewHeading(2)
	heading.SetAttributeString("id", []byte("toc"))
	heading.AppendChild(heading, ast.NewString([]byte("Table of Contents")))

	// insert
	doc.InsertBefore(doc, doc.FirstChild(), list)
	doc.InsertBefore(doc, doc.FirstChild(), heading)
	return nil
}
