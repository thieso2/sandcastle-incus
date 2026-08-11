# Populates git's global user.name/user.email from the gh-authenticated
# account on first login. Only runs when gh already holds a credential and
# neither value has been configured yet, so it never clobbers an identity a
# tenant set by hand — safe to source on every new shell.
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  if [ -z "$(git config --global user.name 2>/dev/null)" ] && [ -z "$(git config --global user.email 2>/dev/null)" ]; then
    gh_name=$(gh api user --jq '.name // .login' 2>/dev/null)
    gh_email=$(gh api user --jq '.email // empty' 2>/dev/null)
    [ -n "$gh_name" ] && git config --global user.name "$gh_name"
    [ -n "$gh_email" ] && git config --global user.email "$gh_email"
  fi
fi
