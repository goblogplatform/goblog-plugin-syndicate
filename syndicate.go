package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Post is one entry of goblog's posts_list host function.
type Post struct {
	ID      uint     `json:"id"`
	Title   string   `json:"title"`
	URL     string   `json:"url"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// kv is the plugin store; the wasm build backs it with store_get/store_set.
type kv interface {
	get(key string) (string, bool)
	set(key, value string) bool
}

// doer sends an HTTP POST; the wasm build uses the PDK, tests a fake.
type doer func(url string, headers map[string]string, body []byte) (status int, respBody []byte)

// channel is one place posts are cross-posted to.
type channel struct {
	name string
	// configured reports whether the admin has filled in its settings.
	configured func(settings map[string]string) bool
	// publish cross-posts p and returns the copy's URL.
	publish func(do doer, settings map[string]string, p Post) (string, error)
}

var channels = []channel{
	{name: "devto", configured: func(s map[string]string) bool { return s["devto_api_key"] != "" }, publish: publishDevTo},
	{name: "hashnode", configured: func(s map[string]string) bool {
		return s["hashnode_token"] != "" && s["hashnode_publication_id"] != ""
	}, publish: publishHashnode},
}

// Store keys: sent/<channel>/<post id> holds the copy's URL ("skipped" for
// posts that predate the channel); baseline/<channel> marks that the
// channel's existing posts have been recorded as skipped.
func sentKey(ch string, id uint) string { return fmt.Sprintf("sent/%s/%d", ch, id) }

// syndicate cross-posts every listed post that a configured channel has not
// seen. The first run for a channel only records the posts already
// published, so turning a channel on never floods it with the back catalogue.
// A failed post is logged and retried on the next run.
func syndicate(do doer, st kv, settings map[string]string, posts []Post, logf func(string)) {
	for _, ch := range channels {
		if !ch.configured(settings) {
			continue
		}
		if _, ok := st.get("baseline/" + ch.name); !ok {
			for _, p := range posts {
				st.set(sentKey(ch.name, p.ID), "skipped")
			}
			st.set("baseline/"+ch.name, "1")
			continue
		}
		for i := len(posts) - 1; i >= 0; i-- { // oldest first, so copies keep the blog's order
			p := posts[i]
			if _, ok := st.get(sentKey(ch.name, p.ID)); ok {
				continue
			}
			if !strings.HasPrefix(p.URL, "http") {
				logf("not cross-posting " + p.Title + ": set the site_url setting so posts have an absolute canonical URL")
				return
			}
			u, err := ch.publish(do, settings, p)
			if err != nil {
				logf(ch.name + ": " + p.Title + ": " + err.Error())
				continue
			}
			if !st.set(sentKey(ch.name, p.ID), u) {
				logf(ch.name + ": could not record " + p.Title + " as sent")
			}
			logf(ch.name + ": cross-posted " + p.Title + " to " + u)
		}
	}
}

// relLink matches a Markdown link/image or an HTML src/href whose target is
// site-relative ("/..."), which would break on another site.
var relLink = regexp.MustCompile(`(\]\(|src="|href=")/([^/])`)

// absolutize rewrites site-relative links in content against the post's URL.
func absolutize(content, postURL string) string {
	u, err := url.Parse(postURL)
	if err != nil || u.Host == "" {
		return content
	}
	return relLink.ReplaceAllString(content, "${1}"+u.Scheme+"://"+u.Host+"/${2}")
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// tagSlugs lowercases tags to letters and digits, drops empty ones and
// keeps at most n.
func tagSlugs(tags []string, n int) []string {
	out := []string{}
	for _, t := range tags {
		if s := nonAlnum.ReplaceAllString(strings.ToLower(t), ""); s != "" && len(out) < n {
			out = append(out, s)
		}
	}
	return out
}

func publishDevTo(do doer, settings map[string]string, p Post) (string, error) {
	body, _ := json.Marshal(map[string]any{"article": map[string]any{
		"title":         p.Title,
		"body_markdown": absolutize(p.Content, p.URL),
		"published":     settings["devto_publish"] == "true",
		"canonical_url": p.URL,
		"tags":          tagSlugs(p.Tags, 4), // dev.to allows four
	}})
	status, resp := do("https://dev.to/api/articles", map[string]string{
		"api-key": settings["devto_api_key"], "Content-Type": "application/json", "Accept": "application/vnd.forem.api-v1+json",
	}, body)
	if status != 201 && status != 200 {
		return "", fmt.Errorf("dev.to returned HTTP %d: %s", status, truncate(string(resp), 200))
	}
	var r struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resp, &r); err != nil || r.URL == "" {
		return "", fmt.Errorf("dev.to: unexpected response: %s", truncate(string(resp), 200))
	}
	return r.URL, nil
}

const hashnodePublish = `mutation Publish($input: PublishPostInput!) { publishPost(input: $input) { post { url } } }`

func publishHashnode(do doer, settings map[string]string, p Post) (string, error) {
	tags := []map[string]string{}
	for _, s := range tagSlugs(p.Tags, 5) {
		tags = append(tags, map[string]string{"slug": s, "name": s})
	}
	body, _ := json.Marshal(map[string]any{"query": hashnodePublish, "variables": map[string]any{"input": map[string]any{
		"title":              p.Title,
		"contentMarkdown":    absolutize(p.Content, p.URL),
		"publicationId":      settings["hashnode_publication_id"],
		"originalArticleURL": p.URL,
		"tags":               tags,
	}}})
	status, resp := do("https://gql.hashnode.com", map[string]string{
		"Authorization": settings["hashnode_token"], "Content-Type": "application/json",
	}, body)
	if status != 200 {
		return "", fmt.Errorf("hashnode returned HTTP %d: %s", status, truncate(string(resp), 200))
	}
	// GraphQL reports failures with HTTP 200 and an errors array.
	var r struct {
		Data struct {
			PublishPost struct {
				Post struct {
					URL string `json:"url"`
				} `json:"post"`
			} `json:"publishPost"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return "", fmt.Errorf("hashnode: unexpected response: %s", truncate(string(resp), 200))
	}
	if len(r.Errors) > 0 {
		return "", fmt.Errorf("hashnode: %s", r.Errors[0].Message)
	}
	if r.Data.PublishPost.Post.URL == "" {
		return "", fmt.Errorf("hashnode: no post URL in response: %s", truncate(string(resp), 200))
	}
	return r.Data.PublishPost.Post.URL, nil
}

// truncate cuts s to at most n runes.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
