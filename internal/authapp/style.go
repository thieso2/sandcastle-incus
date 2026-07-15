package authapp

import "net/http"

// appStylesheet is the minimal shared stylesheet for every Auth App web page. It
// is intentionally small — base typography, a centered container, and lightly
// styled forms/buttons/tables — and adapts to the viewer's light/dark theme.
// Page-specific inline <style> blocks load after it and win by cascade order.
const appStylesheet = `
:root {
  --bg: #f7f7f8; --fg: #1a1a1a; --muted: #6b7280; --border: #d9dbe0;
  --card: #ffffff; --accent: #2563eb; --accent-fg: #ffffff; --code: #eef0f3;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #16181d; --fg: #e6e7ea; --muted: #9aa0aa; --border: #2c2f36;
    --card: #1d2027; --accent: #3b82f6; --accent-fg: #ffffff; --code: #23262d;
  }
}
* { box-sizing: border-box; }
body {
  margin: 0; background: var(--bg); color: var(--fg);
  font: 15px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
}
main {
  max-width: 46rem; margin: 2.5rem auto; padding: 1.75rem 2rem;
  background: var(--card); border: 1px solid var(--border); border-radius: 12px;
}
h1 { font-size: 1.4rem; margin: 0 0 1rem; }
h2 { font-size: 1.1rem; margin: 1.5rem 0 .6rem; }
a { color: var(--accent); }
p { margin: .7rem 0; }
small, .muted { color: var(--muted); }
code { background: var(--code); padding: .1em .35em; border-radius: 5px; font-size: .9em; }
label { display: block; margin: .3rem 0; font-weight: 500; }
input, select {
  display: block; width: 100%; margin-top: .3rem; padding: .5rem .6rem;
  font: inherit; color: var(--fg); background: var(--bg);
  border: 1px solid var(--border); border-radius: 8px;
}
input:focus, select:focus { outline: 2px solid var(--accent); outline-offset: 1px; }
button {
  font: inherit; font-weight: 600; cursor: pointer; margin: .9rem .5rem 0 0;
  padding: .55rem 1.1rem; border: 1px solid var(--border); border-radius: 8px;
  background: var(--card); color: var(--fg);
}
button[value="approve"], button[type="submit"].primary {
  background: var(--accent); color: var(--accent-fg); border-color: transparent;
}
button:hover { filter: brightness(1.05); }
table { border-collapse: collapse; width: 100%; margin: 1rem 0; }
th, td { text-align: left; padding: .5rem .6rem; border-bottom: 1px solid var(--border); }
th { color: var(--muted); font-weight: 600; }
`

// styleCSS serves the shared stylesheet. It is public (no session) so it also
// styles the unauthenticated login and device-approval pages.
func (h handler) styleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(appStylesheet))
}
