//go:build wasip1

// Syndicate is a goblog WebAssembly plugin: an hourly job cross-posts newly
// published posts to dev.to and Hashnode, each copy carrying a canonical
// link back to the original on the blog.
//
// Build:  GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags="-s -w" -o plugin.wasm .
// See goblog's Plugin API reference (goblog.live/docs/plugin-api) for the contract.
package main

import (
	"encoding/json"

	pdk "github.com/extism/go-pdk"
)

//go:wasmimport extism:host/user store_get
func hostStoreGet(uint64) uint64

//go:wasmimport extism:host/user store_set
func hostStoreSet(uint64, uint64) uint64

//go:wasmimport extism:host/user posts_list
func hostPostsList(uint64) uint64

// pdkStore is the plugin store behind the store_* host functions.
type pdkStore struct{}

func (pdkStore) get(key string) (string, bool) {
	k := pdk.AllocateString(key)
	defer k.Free()
	off := hostStoreGet(k.Offset())
	if off == 0 { // Extism's null: missing key
		return "", false
	}
	return pdk.ParamString(off), true
}

func (pdkStore) set(key, value string) bool {
	k := pdk.AllocateString(key)
	defer k.Free()
	v := pdk.AllocateString(value)
	defer v.Free()
	return hostStoreSet(k.Offset(), v.Offset()) == 0
}

// pdkDo is the doer used in the wasm build (always POST): Extism's http_request, limited
// by the host to allowed_hosts.
func pdkDo(url string, headers map[string]string, body []byte) (int, []byte) {
	req := pdk.NewHTTPRequest(pdk.MethodPost, url)
	for k, v := range headers {
		req.SetHeader(k, v)
	}
	if body != nil {
		req.SetBody(body)
	}
	resp := req.Send()
	return int(resp.Status()), resp.Body()
}

// setting mirrors goblog's settings JSON shape.
type setting struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Default     string `json:"default"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

func outputJSON(v any) int32 {
	if err := pdk.OutputJSON(v); err != nil {
		pdk.SetErrorString("encode output: " + err.Error())
		return 1
	}
	return 0
}

//go:wasmexport identity
func identity() int32 {
	return outputJSON(map[string]string{"name": "syndicate", "display_name": "Syndicate", "version": "1.0.0"})
}

//go:wasmexport settings
func settings() int32 {
	return outputJSON([]setting{
		{Key: "enabled", Type: "text", Default: "false", Label: "Enabled", Description: "Set to 'true' to start cross-posting. Posts already published when a channel is first configured are not cross-posted."},
		{Key: "devto_api_key", Type: "password", Label: "dev.to API key", Description: "From dev.to → Settings → Extensions → DEV Community API Keys. Leave empty to skip dev.to."},
		{Key: "devto_publish", Type: "text", Default: "false", Label: "Publish on dev.to", Description: "'true' publishes the copy; anything else leaves it as a dev.to draft for you to review"},
		{Key: "hashnode_token", Type: "password", Label: "Hashnode token", Description: "From hashnode.com → Settings → Developer → Personal Access Token. Leave empty to skip Hashnode."},
		{Key: "hashnode_publication_id", Type: "text", Label: "Hashnode publication ID", Description: "Your blog's ID, from its dashboard URL: hashnode.com/<publication-id>/dashboard"},
	})
}

//go:wasmexport jobs
func jobs() int32 {
	return outputJSON([]map[string]any{{"name": "syndicate", "interval_seconds": 3600}})
}

//go:wasmexport run_job
func runJob() int32 {
	var in struct {
		Name     string            `json:"name"`
		Settings map[string]string `json:"settings"`
	}
	if err := json.Unmarshal(pdk.Input(), &in); err != nil {
		pdk.SetErrorString("run_job: " + err.Error())
		return 1
	}
	if in.Name != "syndicate" {
		return outputJSON(map[string]any{})
	}
	var posts []Post
	if err := json.Unmarshal([]byte(pdk.ParamString(hostPostsList(50))), &posts); err != nil {
		pdk.SetErrorString("posts_list: " + err.Error())
		return 1
	}
	syndicate(pdkDo, pdkStore{}, in.Settings, posts, func(msg string) { pdk.Log(pdk.LogInfo, msg) })
	return outputJSON(map[string]any{})
}

func main() {}
