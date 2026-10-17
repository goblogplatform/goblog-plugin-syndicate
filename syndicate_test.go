package main

import (
	"encoding/json"
	"strings"
	"testing"
)

type memKV map[string]string

func (m memKV) get(k string) (string, bool) { v, ok := m[k]; return v, ok }
func (m memKV) set(k, v string) bool        { m[k] = v; return true }

// fakeAPIs answers dev.to and Hashnode, recording each request body by host.
func fakeAPIs(t *testing.T, sent map[string][]map[string]any) doer {
	return func(url string, headers map[string]string, body []byte) (int, []byte) {
		var b map[string]any
		if err := json.Unmarshal(body, &b); err != nil {
			t.Fatal(err)
		}
		switch url {
		case "https://dev.to/api/articles":
			if headers["api-key"] != "dk" {
				t.Errorf("dev.to api-key = %q", headers["api-key"])
			}
			sent["devto"] = append(sent["devto"], b["article"].(map[string]any))
			return 201, []byte(`{"url":"https://dev.to/me/x"}`)
		case "https://gql.hashnode.com":
			if headers["Authorization"] != "hk" {
				t.Errorf("hashnode Authorization = %q", headers["Authorization"])
			}
			in := b["variables"].(map[string]any)["input"].(map[string]any)
			sent["hashnode"] = append(sent["hashnode"], in)
			return 200, []byte(`{"data":{"publishPost":{"post":{"url":"https://me.hashnode.dev/x"}}}}`)
		}
		t.Fatalf("unexpected request to %s", url)
		return 0, nil
	}
}

var allSettings = map[string]string{"devto_api_key": "dk", "devto_publish": "true", "hashnode_token": "hk", "hashnode_publication_id": "pub"}

func TestSyndicate(t *testing.T) {
	st := memKV{}
	sent := map[string][]map[string]any{}
	do := fakeAPIs(t, sent)
	old := []Post{{ID: 1, Title: "Old", URL: "https://blog.example/posts/2026/01/01/old"}}

	// First run records the back catalogue without posting it.
	syndicate(do, st, allSettings, old, func(s string) { t.Log(s) })
	if len(sent) != 0 || st["sent/devto/1"] != "skipped" || st["sent/hashnode/1"] != "skipped" {
		t.Fatalf("baseline run sent %v, store %v", sent, st)
	}

	// New posts (newest first, as posts_list returns them) go out oldest first.
	posts := append([]Post{
		{ID: 3, Title: "Three", URL: "https://blog.example/posts/2026/02/02/three", Content: "![x](/wp-content/a.png)", Tags: []string{"Go Lang", "wasm", "a", "b", "c"}},
		{ID: 2, Title: "Two", URL: "https://blog.example/posts/2026/02/01/two"},
	}, old...)
	syndicate(do, st, allSettings, posts, func(s string) { t.Log(s) })
	if len(sent["devto"]) != 2 || sent["devto"][0]["title"] != "Two" || sent["devto"][1]["title"] != "Three" {
		t.Fatalf("dev.to got %v", sent["devto"])
	}
	a := sent["devto"][1]
	if a["canonical_url"] != posts[0].URL || a["published"] != true || a["body_markdown"] != "![x](https://blog.example/wp-content/a.png)" {
		t.Errorf("dev.to article = %v", a)
	}
	if tags := a["tags"].([]any); len(tags) != 4 || tags[0] != "golang" {
		t.Errorf("dev.to tags = %v", tags)
	}
	if h := sent["hashnode"][1]; h["originalArticleURL"] != posts[0].URL || h["publicationId"] != "pub" {
		t.Errorf("hashnode input = %v", h)
	}
	if st["sent/devto/3"] != "https://dev.to/me/x" || st["sent/hashnode/2"] != "https://me.hashnode.dev/x" {
		t.Errorf("store = %v", st)
	}

	// Nothing new: nothing sent again.
	syndicate(do, st, allSettings, posts, func(s string) { t.Log(s) })
	if len(sent["devto"]) != 2 || len(sent["hashnode"]) != 2 {
		t.Errorf("re-sent: %d / %d", len(sent["devto"]), len(sent["hashnode"]))
	}
}

func TestSyndicate_UnconfiguredAndRelativeURL(t *testing.T) {
	st := memKV{}
	never := func(string, map[string]string, []byte) (int, []byte) { t.Fatal("no request expected"); return 0, nil }
	syndicate(never, st, map[string]string{"hashnode_token": "hk"}, []Post{{ID: 1, URL: "https://b/x"}}, func(s string) { t.Log(s) })
	if len(st) != 0 {
		t.Errorf("unconfigured channels must not touch the store: %v", st)
	}
	st["baseline/devto"] = "1"
	var logged string
	syndicate(never, st, map[string]string{"devto_api_key": "dk"}, []Post{{ID: 1, Title: "T", URL: "/posts/x"}}, func(s string) { logged = s })
	if !strings.Contains(logged, "site_url") {
		t.Errorf("relative URL should log a site_url hint, got %q", logged)
	}
}

func TestSyndicate_FailureRetries(t *testing.T) {
	st := memKV{"baseline/devto": "1", "baseline/hashnode": "1"}
	calls := 0
	failing := func(url string, _ map[string]string, _ []byte) (int, []byte) {
		calls++
		if url == "https://gql.hashnode.com" {
			return 200, []byte(`{"errors":[{"message":"bad publication"}]}`)
		}
		return 422, []byte(`{"error":"canonical url taken"}`)
	}
	var logs []string
	syndicate(failing, st, allSettings, []Post{{ID: 5, Title: "T", URL: "https://b/x"}}, func(s string) { logs = append(logs, s) })
	if _, ok := st["sent/devto/5"]; ok {
		t.Error("a failed post must not be recorded as sent")
	}
	if _, ok := st["sent/hashnode/5"]; ok {
		t.Error("a GraphQL error must not be recorded as sent")
	}
	if calls != 2 || len(logs) != 2 || !strings.Contains(logs[0], "422") || !strings.Contains(logs[1], "bad publication") {
		t.Errorf("calls=%d logs=%v", calls, logs)
	}
}
