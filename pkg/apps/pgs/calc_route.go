package pgs

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
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

func checkIsRedirect(status int) bool {
	return status >= 300 && status <= 399
}

func correlatePlaceholder(orig, pattern string) (string, string) {
	origList := splitFp(orig)
	patternList := splitFp(pattern)
	nextList := []string{}
	for idx, item := range patternList {
		if len(origList) <= idx {
			continue
		}

		if strings.HasPrefix(item, ":") {
			nextList = append(nextList, origList[idx])
		} else if strings.Contains(item, "*") {
			nextList = append(nextList, strings.ReplaceAll(item, "*", "(.*)"))
		} else if item == origList[idx] {
			nextList = append(nextList, origList[idx])
		} else {
			nextList = append(nextList, item)
			// if we are on the last pattern item then we need to ensure
			// it matches the end of string so partial matches are not counted
			if idx == len(patternList)-1 {
				// regex end of string matcher
				nextList = append(nextList, "$")
			}
		}
	}

	_type := "none"
	if len(nextList) > 0 && len(nextList) == len(patternList) {
		_type = "match"
	} else if strings.Contains(pattern, "*") {
		_type = "wildcard"
		if pattern == "/*" {
			nextList = append(nextList, ".*")
		}
	} else if strings.Contains(pattern, ":") {
		_type = "variable"
	}

	return filepath.Join(nextList...), _type
}

func splitFp(str string) []string {
	ls := strings.Split(str, "/")
	fin := []string{}
	for _, l := range ls {
		if l == "" {
			continue
		}
		fin = append(fin, l)
	}
	return fin
}

func genRedirectRoute(actual string, fromStr string, to string) string {
	if to == "/" {
		return to
	}
	actualList := splitFp(actual)
	fromList := splitFp(fromStr)
	prefix := ""
	var toList []string
	if hasProtocol(to) {
		u, _ := url.Parse(to)
		if u.Path == "" {
			return to
		}
		toList = splitFp(u.Path)
		prefix = u.Scheme + "://" + u.Host
	} else {
		toList = splitFp(to)
	}

	mapper := map[string]string{}
	for idx, item := range fromList {
		if len(actualList) < idx {
			continue
		}

		if strings.HasPrefix(item, ":") {
			mapper[item] = actualList[idx]
		}
		if strings.HasSuffix(item, "*") {
			ls := actualList[idx:]
			// if the * is part of other text in the segment (e.g. `/files*`)
			// then we don't want to include "files" in the destination
			if len(item) > 1 && len(actualList) > idx+1 {
				ls = actualList[idx+1:]
			}
			// standalone splat
			splat := strings.Join(ls, "/")
			mapper[":splat"] = splat

			// splat as a suffix to a string
			place := strings.ReplaceAll(item, "*", ":splat")
			mapper[place] = strings.Join(actualList[idx:], "/")
			break
		}
	}

	fin := []string{"/"}

	for _, item := range toList {
		if strings.HasSuffix(item, ":splat") {
			fin = append(fin, mapper[item])
		} else if mapper[item] != "" {
			fin = append(fin, mapper[item])
		} else {
			fin = append(fin, item)
		}
	}

	result := prefix + filepath.Join(fin...)
	if !strings.HasSuffix(result, "/") && (strings.HasSuffix(to, "/") || strings.HasSuffix(actual, "/")) {
		result += "/"
	}
	return result
}

func calcRoutes(projectName, fp string, userRedirects []*RedirectRule) []*HttpReply {
	rts := []*HttpReply{}
	if !strings.HasPrefix(fp, "/") {
		fp = "/" + fp
	}
	// add route as-is without expansion
	if !strings.HasSuffix(fp, "/") {
		defRoute := shared.GetAssetFileName(&utils.FileEntry{
			Filepath: filepath.Join(projectName, fp),
		})
		rts = append(rts, &HttpReply{Filepath: defRoute, Status: http.StatusOK})
	}
	expts := expandRoute(projectName, fp, http.StatusOK)
	rts = append(rts, expts...)

	// user routes
	for _, redirect := range userRedirects {
		// this doesn't make sense so it is forbidden
		if redirect.From == redirect.To {
			continue
		}

		// hack: make suffix `/` optional when matching
		from := filepath.Clean(redirect.From)
		match := []string{}
		fromMatcher, matcherType := correlatePlaceholder(fp, from)
		switch matcherType {
		case "match":
			fallthrough
		case "wildcard":
			fallthrough
		case "variable":
			rr := regexp.MustCompile(fromMatcher)
			match = rr.FindStringSubmatch(fp)
		case "none":
			fallthrough
		default:
		}

		if len(match) > 0 && match[0] != "" {
			isRedirect := checkIsRedirect(redirect.Status)
			if !isRedirect && !hasProtocol(redirect.To) {
				route := genRedirectRoute(fp, from, redirect.To)
				// wipe redirect rules to prevent infinite loops
				// as such we only support a single hop for user defined redirects
				redirectRoutes := calcRoutes(projectName, route, []*RedirectRule{})
				rts = append(rts, redirectRoutes...)
				return rts
			}

			route := genRedirectRoute(fp, from, redirect.To)
			userReply := []*HttpReply{}
			var rule *HttpReply
			if redirect.To != "" {
				rule = &HttpReply{
					Filepath: route,
					Status:   redirect.Status,
					Query:    redirect.Query,
				}
				userReply = append(userReply, rule)
			}

			if redirect.Force {
				rts = userReply
			} else {
				rts = append(rts, userReply...)
			}

			if hasProtocol(redirect.To) {
				// redirecting to another site so we should bail early
				return rts
			} else {
				// quit after first match
				break
			}
		}
	}

	// we might have a directory so add a trailing slash with a 301
	// we can't check for file extention because route could have a dot
	// and ext parsing gets confused
	if !strings.HasSuffix(fp, "/") {
		redirectRoute := shared.GetAssetFileName(&utils.FileEntry{
			Filepath: fp + "/",
		})
		rts = append(
			rts,
			&HttpReply{Filepath: redirectRoute, Status: http.StatusMovedPermanently},
		)
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
