package pgs

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/send/send/utils"
)

type HttpReply struct {
	Filepath string
	Query    map[string]string
	Status   int
}

func expandRoute(projectName, fp string, status int) []*HttpReply {
	if fp == "" {
		fp = "/"
	}
	mimeType := storage.GetMimeType(fp)
	fname := filepath.Base(fp)
	fdir := filepath.Dir(fp)
	fext := filepath.Ext(fp)
	routes := []*HttpReply{}

	if mimeType != "text/plain" {
		return routes
	}

	if fext == ".txt" {
		return routes
	}

	// we know it's a directory so send the index.html for it
	if strings.HasSuffix(fp, "/") {
		dirRoute := shared.GetAssetFileName(&utils.FileEntry{
			Filepath: filepath.Join(projectName, fp, "index.html"),
		})

		routes = append(
			routes,
			&HttpReply{Filepath: dirRoute, Status: status},
		)
	} else {
		if fname == "." {
			return routes
		}

		// pretty urls where we just append .html to base of fp
		nameRoute := shared.GetAssetFileName(&utils.FileEntry{
			Filepath: filepath.Join(
				projectName,
				fdir,
				fmt.Sprintf("%s.html", fname),
			),
		})

		routes = append(
			routes,
			&HttpReply{Filepath: nameRoute, Status: status},
		)
	}

	return routes
}

func hasProtocol(url string) bool {
	isFullUrl := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	return isFullUrl
}

func calcRoutes(projectName, fp string, userRedirects []*RedirectRule) []*HttpReply {
	rts := expandRoute(projectName, fp, http.StatusOK)
	fext := filepath.Ext(fp)
	// add route as-is without expansion if there is a file ext
	if fp != "" && fext != "" {
		defRoute := shared.GetAssetFileName(&utils.FileEntry{
			Filepath: filepath.Join(projectName, fp),
		})

		rts = append(rts,
			&HttpReply{
				Filepath: defRoute, Status: http.StatusOK,
			},
		)
	}

	// user routes
	for _, redirect := range userRedirects {
		rr := regexp.MustCompile(redirect.From)
		match := rr.FindStringSubmatch(fp)
		if len(match) > 0 {
			userReply := []*HttpReply{}
			ruleRoute := redirect.To

			// special case for redirects that include http(s)://
			isFullUrl := hasProtocol(redirect.To)
			if !isFullUrl {
				ruleRoute = shared.GetAssetFileName(&utils.FileEntry{
					Filepath: filepath.Join(projectName, redirect.To),
				})
			}

			var rule *HttpReply
			if redirect.To != "" && redirect.To != "/" {
				rule = &HttpReply{
					Filepath: ruleRoute,
					Status:   redirect.Status,
					Query:    redirect.Query,
				}
				userReply = append(userReply, rule)
			}

			if !isFullUrl {
				expandedRoutes := expandRoute(projectName, redirect.To, redirect.Status)
				userReply = append(userReply, expandedRoutes...)
			}

			if redirect.Force {
				rts = userReply
			} else {
				rts = append(rts, userReply...)
			}
			// quit after first match
			break
		}
	}

	// filename without extension mean we might have a directory
	// so add a trailing slash with a 301
	if !strings.HasSuffix(fp, "/") && fp != "" && fext == "" {
		redirectRoute := shared.GetAssetFileName(&utils.FileEntry{
			Filepath: fp + "/",
		})
		rts = append(
			rts,
			&HttpReply{Filepath: redirectRoute, Status: http.StatusMovedPermanently},
		)
		// 301 is always actived so anything after this branch will never
		// be executed ... so return early
		// return rts
	}

	notFound := &HttpReply{
		Filepath: filepath.Join(projectName, "404.html"),
		Status:   http.StatusNotFound,
	}

	rts = append(rts,
		notFound,
	)

	return rts
}
