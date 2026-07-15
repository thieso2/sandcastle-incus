package authapp

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

// logsWeb renders the Activity Log page. A Sandcastle Admin sees every log row
// (including system/unauthenticated rows with no user); every other allowlisted
// user sees only rows attributed to her own user key. Access requires an
// allowlisted web session.
func (h handler) logsWeb(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/logs" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, err := h.requireAllowlistedSession(r)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	search := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := DefaultLogListLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 {
			limit = parsed
		}
	}

	page := logsPage{
		User:    user,
		IsAdmin: user.SandcastleAdmin,
		Search:  search,
		Limit:   limit,
	}

	var rows []LogEntry
	if user.SandcastleAdmin {
		rows, err = ListAllLogs(r.Context(), h.db, search, limit)
	} else {
		rows, err = ListLogsForUser(r.Context(), h.db, user.UserKey, search, limit)
	}
	if err != nil {
		page.Error = err.Error()
	}
	for _, row := range rows {
		page.Rows = append(page.Rows, toLogRow(row))
	}
	page.Total = len(page.Rows)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := logsTemplate.Execute(w, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type logsPage struct {
	User    User
	IsAdmin bool
	Search  string
	Limit   int
	Total   int
	Rows    []logRow
	Error   string
}

type logRow struct {
	Time     string
	Level    string
	Kind     string
	Event    string
	Target   string // "METHOD /path" for requests, else the event/detail
	Status   int
	Duration string
	UserKey  string
	Detail   string
	LevelCSS string
}

func toLogRow(e LogEntry) logRow {
	target := strings.TrimSpace(e.Method + " " + e.Path)
	if target == "" {
		target = e.Event
	}
	return logRow{
		Time:     e.Time.UTC().Format("2006-01-02 15:04:05"),
		Level:    e.Level,
		Kind:     e.Kind,
		Event:    e.Event,
		Target:   target,
		Status:   e.Status,
		Duration: formatLogDuration(e.DurationMS),
		UserKey:  e.UserKey,
		Detail:   e.Detail,
		LevelCSS: strings.ToLower(e.Level),
	}
}

func formatLogDuration(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	return strconv.FormatFloat(float64(ms)/1000, 'f', 2, 64) + "s"
}

var logsTemplate = template.Must(template.New("logs").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <link rel="stylesheet" href="/style.css">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sandcastle activity log</title>
  <style>
    :root { color-scheme: light dark; }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: #f5f5f7;
      color: #1d1d1f;
    }
    @media (prefers-color-scheme: dark) {
      body { background: #000; color: #f5f5f7; }
      .card, table { background: #1c1c1e; }
      tbody tr:nth-child(even) { background: #161618; }
      .meta { color: #a1a1a6; }
      th { color: #a1a1a6; border-color: #2c2c2e; }
      td { border-color: #2c2c2e; }
    }
    header {
      padding: 16px;
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 8px;
      flex-wrap: wrap;
    }
    h1 { font-size: 20px; margin: 0; }
    .who { font-size: 13px; opacity: 0.7; }
    main { padding: 0 12px 32px; max-width: 1000px; margin: 0 auto; }
    .bar { display: flex; gap: 8px; align-items: center; margin: 8px 4px 16px; flex-wrap: wrap; }
    .bar input[type=search] {
      flex: 1 1 200px;
      padding: 9px 12px;
      border-radius: 10px;
      border: 1px solid #c7c7cc;
      font: inherit;
      background: transparent;
      color: inherit;
    }
    .bar button {
      padding: 9px 16px;
      border-radius: 10px;
      border: none;
      background: #0071e3;
      color: #fff;
      font: inherit;
      font-weight: 600;
      cursor: pointer;
    }
    .scope { font-size: 13px; opacity: 0.65; }
    .tablewrap { overflow-x: auto; border-radius: 12px; }
    table { width: 100%; border-collapse: collapse; background: #fff; font-size: 13px; }
    th, td { text-align: left; padding: 8px 10px; border-bottom: 1px solid #e5e5ea; white-space: nowrap; }
    th { font-weight: 600; text-transform: uppercase; letter-spacing: 0.04em; font-size: 11px; opacity: 0.6; }
    td.detail { white-space: normal; word-break: break-word; opacity: 0.8; }
    .tag { font-size: 11px; padding: 1px 6px; border-radius: 6px; background: #e5e5ea; color: #3a3a3c; }
    @media (prefers-color-scheme: dark) { .tag { background: #2c2c2e; color: #d1d1d6; } }
    .lvl-error { color: #d70015; font-weight: 600; }
    .lvl-warn { color: #b25000; font-weight: 600; }
    @media (prefers-color-scheme: dark) { .lvl-error { color: #ff6961; } .lvl-warn { color: #ffb340; } }
    .empty { text-align: center; opacity: 0.6; margin-top: 48px; }
    .error { background: #ffe5e5; color: #8a1f1f; border-radius: 10px; padding: 12px 14px; margin: 12px 4px; }
    @media (prefers-color-scheme: dark) { .error { background: #3a1212; color: #ff9d9d; } }
    a.back { font-size: 13px; }
  </style>
</head>
<body>
  <header>
    <h1>Activity log</h1>
    <span class="who">{{.User.GitHubUsername}}{{if .IsAdmin}} · admin{{end}}</span>
  </header>
  <main>
    <p><a class="back" href="/">← Back</a></p>
    <form class="bar" method="get" action="/logs">
      <input type="search" name="q" placeholder="Filter event, path, detail…" value="{{.Search}}">
      <input type="hidden" name="limit" value="{{.Limit}}">
      <button type="submit">Filter</button>
      <span class="scope">{{if .IsAdmin}}Showing all users' activity.{{else}}Showing only your activity.{{end}} {{.Total}} rows.</span>
    </form>
    {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
    {{if not .Total}}
      {{if not .Error}}<p class="empty">No log entries{{if .Search}} match “{{.Search}}”{{end}}.</p>{{end}}
    {{else}}
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th>Time (UTC)</th>
              <th>Level</th>
              <th>Kind</th>
              <th>Event / Request</th>
              <th>Status</th>
              <th>Duration</th>
              {{if $.IsAdmin}}<th>User</th>{{end}}
              <th>Detail</th>
            </tr>
          </thead>
          <tbody>
            {{range .Rows}}
              <tr>
                <td>{{.Time}}</td>
                <td class="lvl-{{.LevelCSS}}">{{.Level}}</td>
                <td><span class="tag">{{.Kind}}</span></td>
                <td>{{.Target}}</td>
                <td>{{if .Status}}{{.Status}}{{else}}—{{end}}</td>
                <td>{{.Duration}}</td>
                {{if $.IsAdmin}}<td>{{if .UserKey}}{{.UserKey}}{{else}}<span class="meta">system</span>{{end}}</td>{{end}}
                <td class="detail">{{.Detail}}</td>
              </tr>
            {{end}}
          </tbody>
        </table>
      </div>
    {{end}}
  </main>
</body>
</html>
`))
