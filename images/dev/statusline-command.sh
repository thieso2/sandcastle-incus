#!/bin/sh
input=$(cat)

# Single jq invocation: pull every field we need out of stdin at once.
# Missing rate_limit fields are mapped to "" (not `empty`) so the field
# count stays fixed regardless of which ones are present. Fields are
# joined with ASCII unit separator (0x1F) rather than a tab: tab is
# classified as IFS whitespace, which would collapse consecutive empty
# fields during word splitting below and misalign everything after them.
raw=$(printf '%s' "$input" | jq -r '[
  .model.display_name,
  (.context_window.used_percentage // 0),
  .workspace.current_dir,
  (.rate_limits.five_hour.used_percentage // ""),
  (.rate_limits.five_hour.resets_at // ""),
  (.rate_limits.seven_day.used_percentage // "")
] | map(tostring) | join("\u001f")')

oldIFS=$IFS
IFS=$(printf '\037')
set -f
set -- $raw
set +f
IFS=$oldIFS
model=$1
used_pct=$2
cwd=$3
five_pct=$4
five_reset=$5
week_pct=$6

# Show current dir relative to $HOME as ~/...
dir_display=$cwd
case "$dir_display" in
  "$HOME"/*) dir_display="~${dir_display#$HOME}" ;;
  "$HOME") dir_display="~" ;;
esac

branch=$(git -C "$cwd" --no-optional-locks branch --show-current 2>/dev/null)

# Round a percentage to the nearest integer, clamped to [0, 100].
round_pct() {
  awk -v p="$1" 'BEGIN { v = p + 0.5; if (v < 0) v = 0; if (v > 100) v = 100; printf "%d", v }'
}

# Color ramp shared by context usage and rate-limit segments:
# green <70%, yellow 70-89%, red >=90%.
color_for() {
  v=$1
  if [ "$v" -lt 70 ]; then
    printf '\033[32m'
  elif [ "$v" -lt 90 ]; then
    printf '\033[33m'
  else
    printf '\033[31m'
  fi
}

reset='\033[0m'
cyan='\033[36m'
magenta='\033[35m'

used_int=$(round_pct "$used_pct")
color=$(color_for "$used_int")

# Build a 10-segment progress bar
segments=10
filled=$(( used_int / (100 / segments) ))
[ "$filled" -gt "$segments" ] && filled=$segments
empty=$(( segments - filled ))

bar=""
i=0
while [ "$i" -lt "$filled" ]; do bar="${bar}█"; i=$((i + 1)); done
i=0
while [ "$i" -lt "$empty" ]; do bar="${bar}░"; i=$((i + 1)); done

printf '%s %b%s %s%%%b' "$model" "$color" "$bar" "$used_int" "$reset"
printf ' %b\xef\x81\xbc %s%b' "$cyan" "$dir_display" "$reset"
if [ -n "$branch" ]; then
  printf ' %b\xee\x82\xa0 %s%b' "$magenta" "$branch" "$reset"
fi

# Rate-limit segments (Claude.ai Pro/Max subscribers only); omitted entirely
# when the corresponding window is absent from the input JSON.
if [ -n "$five_pct" ]; then
  five_int=$(round_pct "$five_pct")
  five_color=$(color_for "$five_int")
  reset_display=""
  if [ -n "$five_reset" ]; then
    reset_display=$(date -r "$five_reset" +%H:%M 2>/dev/null || date -d "@$five_reset" +%H:%M 2>/dev/null)
  fi
  printf ' %b\xe2\x8f\xb3 5h %s%%%b' "$five_color" "$five_int" "$reset"
  if [ -n "$reset_display" ]; then
    printf ' \xe2\x86\xbb %s' "$reset_display"
  fi
fi

if [ -n "$week_pct" ]; then
  week_int=$(round_pct "$week_pct")
  week_color=$(color_for "$week_int")
  printf ' %b\xf0\x9f\x93\x85 7d %s%%%b' "$week_color" "$week_int" "$reset"
fi
