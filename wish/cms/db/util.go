package db

import "strings"

func FilterMetaFiles(posts []*Post) []*Post {
	filtered := []*Post{}
	for _, post := range posts {
		if strings.HasPrefix(post.Filename, "_") {
			continue
		}
		filtered = append(filtered, post)
	}
	return filtered
}
