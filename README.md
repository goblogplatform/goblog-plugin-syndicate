# goblog-plugin-syndicate

A [goblog](https://github.com/goblogplatform/goblog) WebAssembly plugin that cross-posts your new posts to [dev.to](https://dev.to) and [Hashnode](https://hashnode.com). Every copy carries a [canonical link](https://developers.google.com/search/docs/crawling-indexing/consolidate-duplicate-urls) back to the original on your blog, so search engines credit your site rather than the copy.

## Install

From your goblog's **Admin → Plugins → Browse**, search for *Syndicate* and click **Install** (goblog 0.16.0 or newer, the first release with the `posts_list` host function). Or download `plugin.wasm` from the [latest release](https://github.com/goblogplatform/goblog-plugin-syndicate/releases/latest) into `plugins/wasm/syndicate.wasm`, write `plugins/wasm/syndicate.json` containing `{"allowed_hosts":["dev.to","gql.hashnode.com"]}`, and restart goblog.

Set goblog's **Site URL** setting first: it is what the canonical links point to.

## Settings

Under **Admin → Plugins → Syndicate**. A channel is used once its settings are filled in; leave them empty to skip it.

| Setting | Default | Meaning |
|---|---|---|
| `enabled` | `false` | Set to `true` to start cross-posting |
| `devto_api_key` | | dev.to → Settings → Extensions → *DEV Community API Keys* |
| `devto_publish` | `false` | `true` publishes the dev.to copy; otherwise it is created as a draft for you to review and publish there |
| `hashnode_token` | | hashnode.com → Settings → Developer → *Personal Access Token* |
| `hashnode_publication_id` | | Your Hashnode blog's ID, from its dashboard URL `hashnode.com/<publication-id>/dashboard` |

## How it works

Once an hour a `syndicate` job reads the newest 50 published posts from goblog and sends each one a channel has not had yet: the title, the Markdown with site-relative links and images made absolute, and the tags (lowercased to letters and digits; dev.to keeps four, Hashnode five). Hashnode copies are published right away.

- **No back catalogue flood.** The first run after a channel is configured only records the posts already published. From then on, only posts published later are cross-posted, oldest first.
- **Each post once per channel.** The copy's URL is kept in the plugin store (`sent/<channel>/<post id>`). Editing a post on the blog does not update the copies.
- **Failures retry.** A rejected or failed request is logged under `plugin syndicate:` and retried next hour; nothing is recorded until it succeeds.

The plugin talks to `dev.to` and `gql.hashnode.com` only, and goblog's sandbox blocks every other host. Medium is not supported: it no longer issues API tokens. DZone has no public API.

## Build it yourself

```bash
go test ./...   # unit tests run natively
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -ldflags="-s -w" -o plugin.wasm .
```

Check the result with `goblog validate-plugin plugin.wasm`. Tag a release as `vX.Y.Z`; the workflow builds and uploads `plugin.wasm` for you.

## License

Apache-2.0.
