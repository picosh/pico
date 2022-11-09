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
	"github.com/yuin/goldmark/renderer"
	ghtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
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
	Layout      string
	Image       string
	ImageCard   string
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

type ImgRender struct {
	ghtml.Config
	ImgURL func(url []byte) []byte
}

func NewImgsRenderer(url func([]byte) []byte) renderer.NodeRenderer {
	return &ImgRender{
		Config: ghtml.NewConfig(),
		ImgURL: url,
	}
}

func (r *ImgRender) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindImage, r.renderImage)
}

func (r *ImgRender) renderImage(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	_, _ = w.WriteString("<img src=\"")
	if r.Unsafe || !ghtml.IsDangerousURL(n.Destination) {
		dest := r.ImgURL(n.Destination)
		_, _ = w.Write(util.EscapeHTML(util.URLEscape(dest, true)))
	}
	_, _ = w.WriteString(`" alt="`)
	_, _ = w.Write(util.EscapeHTML(n.Text(source)))
	_ = w.WriteByte('"')
	if n.Title != nil {
		_, _ = w.WriteString(` title="`)
		r.Writer.Write(w, n.Title)
		_ = w.WriteByte('"')
	}
	if n.Attributes() != nil {
		ghtml.RenderAttributes(w, n, ghtml.ImageAttributeFilter)
	}
	if r.XHTML {
		_, _ = w.WriteString(" />")
	} else {
		_, _ = w.WriteString(">")
	}
	return ast.WalkSkipChildren, nil
}

func ParseText(text string, linkify Linkify) (*ParsedText, error) {
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
			extension.Footnote,
			meta.Meta,
			hili,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			ghtml.WithUnsafe(),
			renderer.WithNodeRenderers(
				util.Prioritized(NewImgsRenderer(CreateImgURL(linkify)), 0),
			),
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
	if strings.HasPrefix(parsed.Image, "/") {
		parsed.Image = linkify.Create(parsed.Image)
	} else if strings.HasPrefix(parsed.Image, "./") {
		parsed.Image = linkify.Create(parsed.Image[1:])
	}
	parsed.MetaData.ImageCard = toString(metaData["card"])
	parsed.MetaData.Hidden = toBool(metaData["draft"])

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
