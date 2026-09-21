# Prior art: path navigation, globbing and completion in resource CLIs

Research for wayfinder map #190 / ticket #191 (2026-09-21). Question: how do
CLIs that expose a hierarchy of remote objects let users stand in it, list
it, glob over it and tab-complete it — and what should Sandcastle borrow for
`/remote/tenant/project/machine`? Sources are the tools' own manuals and
upstream source; items that could not be verified are marked.

## Summary table

| Tool | cwd? | `cd` validates | Position stored | Globbing | Completion | Above the leaf level |
|---|---|---|---|---|---|---|
| lftp | remote + local | yes, unless cached-known or `cd dir &` (`cmd:verify-path*`) | `~/.local/share/lftp/cwd_history` per site, bookmarks | client (`glob`, `cls`); `ls` cached (`cache:expire`) | in-process readline; remote `GlobURL` listing | n/a |
| sftp (OpenSSH) | remote | yes: `realpath` + `stat` + `S_ISDIR` | process memory only | client: libc `glob(3)` over SFTP READDIR | in-process editline, uncached `sftp_glob` per Tab | n/a |
| rclone | no (alias backend = fixed cd) | n/a | none | client-side filter rules after listing; shell globs positionals | `rclone __complete` (cobra): remotes from config, then `f.List()` per Tab | `lsd remote:` lists buckets |
| s3cmd | no | n/a | none | client `fnmatch` on URIs in transfer commands; regex filters | none found | `s3cmd ls` lists buckets |
| MinIO `mc` | no | n/a | aliases only (`~/.mc/config.json`) | none (literal/prefix); `find --name` client-side | posener/complete re-exec, live `List` per Tab | `mc ls ALIAS` lists buckets; `cp` to alias root refused |
| gsutil | no | n/a | default project (boto/gcloud property) | server-side prefix + `/` delimiter, client regex; `**` delimiter-less | argcomplete; `~/.gsutil/tab-completion/cache`, 15 s TTL, 5 s timeout, 1000 results | no arg → buckets; `-d` prints the prefix |
| gcloud storage | no | n/a | `core/project` property, named configurations under `~/.config/gcloud` | same rules; `**` "does not match prefixes"; no `-d` | resource cache (1 h) for flags, none for `gs://` paths | no arg → buckets in default project |
| aws s3 | no | n/a | profile in `~/.aws/{config,credentials}` | none in `ls`; `--include/--exclude` client-side on cp/sync | v2 API-backed for `--bucket` etc., uncached; not for `s3://` | no URI → buckets; `PRE x/` markers |
| az storage blob | no | n/a | `az account set`, `defaults.*`, `[storage] account` | none; server-side `--prefix`/`--delimiter` | argcomplete, live API per keystroke | container required |
| vault kv | no | n/a | `VAULT_NAMESPACE` only | none; per-folder LIST, client filter | `complete -C` binary callback, live LIST, uncached | `kv list` on a leaf: "No value found" |
| pulumi | project from cwd walk-up, stack persisted | n/a | `~/.pulumi/workspaces/<proj>-<sha1>-workspace.json`, `PULUMI_STACK` overrides | none | cobra script (dynamic stack names unverified) | "no stack selected; please use `pulumi stack select`" |
| flyctl | no | n/a | `-a` > `FLY_APP` > `fly.toml` in cwd | none | cobra `__complete`, live API, uncached | error naming the flag |
| nomad | namespace/region | n/a | `-namespace` > `NOMAD_NAMESPACE` > `default`; `*` = all | server-side string prefix on `var list` | `complete -C`, live `/v1/search`, cap 20 | — |
| kubectl | context → namespace | n/a | kubeconfig `current-context` + context namespace | `-A` | cobra `__complete`, live `get`, names uncached (types via discovery cache) | namespace defaults to `default` |
| kubectx/kubens | — | — | previous ctx in `${XDG_CACHE_HOME:-~/.kube}/kubectx`, previous ns per context | — | static | — |
| kubie | per-shell | — | temp kubeconfig via `KUBECONFIG`, `KUBIE_*` env | — | — | — |
| terraform | workspace | — | `.terraform/environment`; `TF_WORKSPACE` overrides | — | — | — |

## Findings by theme

### Stateful `cd` is rare, and where it exists it validates

Only the file-transfer shells (lftp, sftp) have a remote `cd`. Both check the
target: sftp does `realpath` + `stat` + `S_ISDIR` (three round trips) and
fails immediately; lftp checks unless the path is already known to its
listing cache (`cmd:verify-path-cached`) or the user backgrounds the `cd`
with `&`. lftp keeps `cd -` per site *on disk* (`cwd_history`, default on),
so it survives a restart; sftp keeps nothing.

Every cloud/resource CLI instead has a *default scope* set by flag → env →
file: gcloud's `core/project`, aws's profile, az's `defaults.*`, nomad's
`NOMAD_NAMESPACE`, kubectl's `current-context`+namespace. The file-backed
ones (kubectl, pulumi, terraform) are global to every shell sharing the
file; kubie and `kubectx -s` exist precisely to make the position
per-terminal via a temp kubeconfig exported in `KUBECONFIG`. Pulumi's
position is two-level: the *project* comes from walking up from cwd to
`Pulumi.yaml` (like `.sandcastle`), the *stack* from a per-project file
under `~/.pulumi/workspaces/` keyed by a hash of the project path.

Standing "above the leaf" is handled three ways: list the level (`gsutil
ls`, `aws s3 ls`, `mc ls ALIAS`, `rclone lsd remote:` all list buckets with
no path), refuse with guidance (pulumi: "no stack selected; please use
`pulumi stack select`"; flyctl: "add an app field to fly.toml or specify
with -a"; `mc cp` to an alias root: "does not contain bucket name"), or
silently default (kubectl's `default` namespace). Nobody silently creates.

### Globbing: server-side prefix, client-side match

The object stores split a pattern at its first metacharacter: the literal
prefix goes to the server (`prefix=` + `delimiter=/`), the rest is matched
client-side with a regex/fnmatch (gsutil `_BuildBucketFilterStrings`,
s3cmd `fetch_remote_list`). `**` is implemented as a *delimiter-less*
listing of the whole subtree filtered locally; gcloud documents that `**`
"does not match prefixes" and that `dir**` degrades to `dir*`. Bucket-name
wildcards are always client-side over `ListBuckets`. The file shells glob
client-side over READDIR (sftp's `glob(3)` with SFTP callbacks; lftp's
`glob`/`cls`). mc has *no* globbing (a `*` is a literal object name unless
`--recursive`, where it becomes a prefix) and documents that as a design
choice; rclone globs nothing positionally and applies filter rules to the
listing. Vault and nomad expose only prefix lists.

Multi-directory output: gsutil and gcloud print each matched directory's
contents under its own header ("Recursive listings … include line breaks
and header formatting for each subdirectory"); gsutil's `-d` prints the
matching directory names instead of their contents.

### Completion: live per keystroke, one cache in the field

Every dynamic completer surveyed re-executes the binary per Tab (cobra
`__complete` in rclone/flyctl/kubectl/pulumi; posener/complete `complete -C`
in mc/vault/nomad; argcomplete in gsutil/gcloud/aws/az) and, with one
exception, queries the service live and uncached: rclone `f.List()`, mc
`clnt.List`, vault `Logical().List()`, nomad `/v1/search` (capped at 20),
kubectl `get` via a go-template, az `list_containers`/`list_blobs`. The
exception is gsutil: a JSON cache under `~/.gsutil/tab-completion/cache`
with a 15 s TTL, a 5 s listing timeout and a 1000-result cap. gcloud has a
1 h resource cache for flag values but none for `gs://` paths. kubectl
caches only resource *types* (discovery) under `~/.kube/cache`. Two
practical caveats recur: completion runs before flag parsing, so it must
rely on env/config for the endpoint (vault); and the completion script is
bound to the binary's `argv[0]`, so an alias name breaks it (flyctl #3519).

### Lessons for Sandcastle

1. **A validated, stateful `cd` with `cd -` is lftp's model, not the cloud
   CLIs'.** Validate like sftp (the target must exist), keep the previous
   position on disk like lftp's `cwd_history`, and offer an unvalidated
   escape (`--local-only`, lftp's `cd dir &`).
2. **File-backed position walked up from cwd** is pulumi/terraform
   territory and matches `.sandcastle`; a per-terminal position is a
   separate layer (kubie) and can stay in the fog.
3. **Above a leaf-holding level, list or refuse — never default.** Listing
   the level (`gsutil ls` → buckets) for `ls`, and pulumi-style refusal
   with the exact next command for creating verbs.
4. **Glob level by level, `**` as a delimiter-less walk**, headers per
   matched directory and `-d` for names only, straight from gsutil; note
   gcloud's `dir**` → `dir*` degradation as the rule for an embedded `**`.
5. **Completion: dynamic, budgeted, from the cheapest source per level**
   (local config for remotes, the Auth App resource cache for the rest),
   with a per-keystroke timeout like gsutil's 5 s. A small on-disk cache
   with a short TTL (gsutil's 15 s) is the only proven improvement and can
   be added later; keep the `argv[0]` binding in mind for the `sc`/
   `sandcastle` symlinks.

## Unverified

lftp defaults for `cache:expire`/`cache:size`/`cmd:verify-path*` and `~`
in remote `cd`; whether lftp completion hits its listing cache; s3cmd's
pattern-compile function and any completion; rclone `ncdu` on bucket
backends and when its path completion landed; gcloud's `interactive`/`meta
cache` GA pages (404, taken from source); pulumi dynamic stack completion;
the bash `kubens` state layout.
