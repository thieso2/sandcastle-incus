# Issue Tracker: GitHub

Issues and PRDs for this repo live as GitHub issues in `thieso2/sandcastle-incus`.
Use the `gh` CLI for issue operations.

## Conventions

- Create an issue: `gh issue create --title "..." --body "..."`
- Read an issue: `gh issue view <number> --comments`
- List issues: `gh issue list --state open --json number,title,body,labels,comments`
- Comment on an issue: `gh issue comment <number> --body "..."`
- Apply labels: `gh issue edit <number> --add-label "..."`
- Remove labels: `gh issue edit <number> --remove-label "..."`
- Close an issue: `gh issue close <number> --comment "..."`

Run `gh` commands from inside this repository so the repo is inferred from the
Git remote.

## Publishing

When a skill says to publish a PRD, plan, or task to the issue tracker, create a
GitHub issue in `thieso2/sandcastle-incus`.

## Fetching

When a skill says to fetch the relevant ticket, run:

```bash
gh issue view <number> --comments
```

