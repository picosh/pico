package tui

import (
	"fmt"
	"sort"
	"strings"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/vxfw"
	"git.sr.ht/~rockorager/vaxis/vxfw/list"
	"git.sr.ht/~rockorager/vaxis/vxfw/richtext"
	"git.sr.ht/~rockorager/vaxis/vxfw/text"
	"github.com/picosh/pico/pkg/db"
)

type PagesLoaded struct{}

type PagesPage struct {
	shared *SharedModel

	list    *list.Dynamic
	pages   []*db.Project
	loading bool
	err     error
}

func NewPagesPage(shrd *SharedModel) *PagesPage {
	page := &PagesPage{
		shared: shrd,
	}
	page.list = &list.Dynamic{Builder: page.getWidget, DrawCursor: true, Gap: 1}
	return page
}

func (m *PagesPage) Footer() []Shortcut {
	return []Shortcut{
		{Shortcut: "c", Text: "copy url"},
		{Shortcut: "^r", Text: "refresh"},
	}
}

func (m *PagesPage) HandleEvent(ev vaxis.Event, phase vxfw.EventPhase) (vxfw.Command, error) {
	switch msg := ev.(type) {
	case PageIn:
		m.loading = true
		go m.fetchPages()
		return vxfw.FocusWidgetCmd(m.list), nil
	case PagesLoaded:
		return vxfw.RedrawCmd{}, nil
	case vaxis.Key:
		if msg.Matches('c') {
			cursor := m.list.Cursor()
			if int(cursor) < len(m.pages) {
				project := m.pages[cursor]
				url := fmt.Sprintf("https://%s-%s.pgs.sh", m.shared.User.Name, project.Name)
				return vxfw.CopyToClipboardCmd(url), nil
			}
		}
		if msg.Matches('r', vaxis.ModCtrl) {
			m.loading = true
			go m.fetchPages()
			return vxfw.RedrawCmd{}, nil
		}
	}
	return nil, nil
}

func (m *PagesPage) fetchPages() {
	if m.shared.User == nil {
		m.err = fmt.Errorf("no user found")
		m.loading = false
		m.shared.App.PostEvent(PagesLoaded{})
		return
	}

	if m.shared.PgsDB == nil {
		m.err = fmt.Errorf("pgs database not configured")
		m.loading = false
		m.shared.App.PostEvent(PagesLoaded{})
		return
	}

	pages, err := m.shared.PgsDB.FindProjectsByUser(m.shared.User.ID)

	m.loading = false
	if err != nil {
		m.err = err
		m.pages = []*db.Project{}
	} else {
		m.err = nil
		sort.Slice(pages, func(i, j int) bool {
			return pages[i].Name < pages[j].Name
		})
		m.pages = pages
	}

	m.shared.App.PostEvent(PagesLoaded{})
}

func (m *PagesPage) Draw(ctx vxfw.DrawContext) (vxfw.Surface, error) {
	w := ctx.Max.Width
	h := ctx.Max.Height
	root := vxfw.NewSurface(w, h, m)
	ah := 0

	pagesLen := len(m.pages)
	err := m.err

	info := text.New("Sites deployed to pgs.  Each page is accessible via https://{user}-{project}.pgs.sh.  We do not have access to custom domains so those cannot be shown.")
	brd := NewBorder(info)
	brd.Label = "desc"
	brdSurf, _ := brd.Draw(createDrawCtx(ctx, 5))
	root.AddChild(0, ah, brdSurf)
	ah += int(brdSurf.Size.Height)

	if err != nil {
		txt := text.New(fmt.Sprintf("Error: %s", err.Error()))
		txt.Style = vaxis.Style{Foreground: red}
		txtSurf, _ := txt.Draw(ctx)
		root.AddChild(0, ah, txtSurf)
	} else if pagesLen == 0 {
		txt := text.New("No pages found.")
		txtSurf, _ := txt.Draw(ctx)
		root.AddChild(0, ah, txtSurf)
	} else {
		listPane := NewBorder(m.list)
		listPane.Label = "pages"
		listPane.Style = vaxis.Style{Foreground: oj}
		listSurf, _ := listPane.Draw(createDrawCtx(ctx, ctx.Max.Height-uint16(ah)))
		root.AddChild(0, ah, listSurf)
	}

	return root, nil
}

func (m *PagesPage) getWidget(i uint, cursor uint) vxfw.Widget {
	if int(i) >= len(m.pages) {
		return nil
	}

	isSelected := i == cursor
	return pageToWidget(m.pages[i], m.shared.User.Name, isSelected)
}

func pageToWidget(project *db.Project, username string, isSelected bool) vxfw.Widget {
	url := fmt.Sprintf("https://%s-%s.pgs.sh", username, project.Name)

	updatedAt := ""
	if project.UpdatedAt != nil {
		updatedAt = project.UpdatedAt.Format("2006-01-02 15:04:05")
	}

	labelStyle := vaxis.Style{Foreground: grey}
	if isSelected {
		labelStyle = vaxis.Style{Foreground: fuschia}
	}

	segs := []vaxis.Segment{
		{Text: "URL: ", Style: labelStyle},
		{Text: url + "\n", Style: vaxis.Style{Foreground: green}},

		{Text: "Updated: ", Style: labelStyle},
		{Text: updatedAt},
	}

	if project.ProjectDir != project.Name {
		segs = append(segs,
			vaxis.Segment{Text: "\n"},
			vaxis.Segment{Text: "Links To: ", Style: labelStyle},
			vaxis.Segment{Text: project.ProjectDir, Style: vaxis.Style{Foreground: purp}},
		)
	}

	if project.Acl.Type != "" && project.Acl.Type != "public" {
		aclStr := project.Acl.Type
		if len(project.Acl.Data) > 0 {
			aclStr += " (" + strings.Join(project.Acl.Data, ", ") + ")"
		}
		segs = append(segs,
			vaxis.Segment{Text: "\n"},
			vaxis.Segment{Text: "ACL: ", Style: labelStyle},
			vaxis.Segment{Text: aclStr, Style: vaxis.Style{Foreground: oj}},
		)
	}

	if project.Blocked != "" {
		segs = append(segs,
			vaxis.Segment{Text: "\n"},
			vaxis.Segment{Text: "Blocked: ", Style: labelStyle},
			vaxis.Segment{Text: project.Blocked, Style: vaxis.Style{Foreground: red}},
		)
	}

	txt := richtext.New(segs)
	return txt
}
