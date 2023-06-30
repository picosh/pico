package pgs

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/picosh/pico/shared"
)

type PageData struct {
	Site shared.SitePageData
}

type Page struct {
	src  string
	dest string
	cfg  *shared.ConfigSite
}

func genPage(page *Page) error {
	ts, err := shared.RenderTemplate(page.cfg, []string{page.cfg.StaticPath(page.src)})

	if err != nil {
		page.cfg.Logger.Error(err)
		return err
	}

	data := PageData{
		Site: *page.cfg.GetSiteData(),
	}
	buf := new(bytes.Buffer)
	err = ts.Execute(buf, data)
	if err != nil {
		page.cfg.Logger.Error(err)
		return err
	}

	err = os.WriteFile(page.dest, buf.Bytes(), 0644)
	if err != nil {
		page.cfg.Logger.Fatal(err)
	}

	return nil
}

func GenStaticSite(dir string, cfg *shared.ConfigSite) error {
	pages := [][2]string{
		{"html/marketing.page.tmpl", "index.html"},
		{"html/ops.page.tmpl", "ops.html"},
		{"html/privacy.page.tmpl", "privacy.html"},
		{"html/help.page.tmpl", "help.html"},
	}
	for _, page := range pages {
		err := genPage(&Page{
			src:  page[0],
			dest: filepath.Join(dir, page[1]),
			cfg:  cfg,
		})
		if err != nil {
			return err
		}
	}

	return nil
}
